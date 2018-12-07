package splitlog

import (
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"sync"
	"time"
)

// These flags define which text to prefix to each log entry generated by the logger.
const (
	// Bits or'ed together to control what's printed. There is no control over the
	// order they appear (the order listed here) or the format they present (as
	// described in the comments).  A colon appears after these items:
	//	2009/01/23 01:23:23.123123 /a/b/c/d.go:23: message
	Ldate         = 1 << iota     // the date: 2009/01/23
	Ltime                         // the time: 01:23:23
	Lmicroseconds                 // microsecond resolution: 01:23:23.123123.  assumes Ltime.
	Llongfile                     // full file name and line number: /a/b/c/d.go:23
	Lshortfile                    // final file name element and line number: d.go:23. overrides Llongfile
	LstdFlags     = Ldate | Ltime // initial values for the standard logger
)

// A logger represents an active logging object that generates lines of
// output to an io.Writer.  Each logging operation makes a single call to
// the Writer's Write method.  A logger can be used simultaneously from
// multiple goroutines; it guarantees to serialize access to the Writer.
type Logger struct {
	mu     sync.Mutex     // ensures atomic writes; protects the following fields
	prefix string         // prefix to write at beginning of each line
	flag   int            // properties
	out    io.WriteCloser // destination for output
	buf    []byte         // for accumulating text to write
}

// New creates a new logger.   The out variable sets the
// destination to which log data will be written.
// The prefix appears at the beginning of each generated log line.
// The flag argument defines the logging properties.
func New(out io.WriteCloser, prefix string, flag int) *Logger {
	return &Logger{out: out, prefix: prefix, flag: flag}
}

var std = New(os.Stderr, "", LstdFlags)

// Cheap integer to fixed-width decimal ASCII.  Give a negative width to avoid zero-padding.
// Knows the buffer has capacity.
func itoa(buf *[]byte, i int, wid int) {
	var u uint = uint(i)
	if u == 0 && wid <= 1 {
		*buf = append(*buf, '0')
		return
	}

	// Assemble decimal in reverse order.
	var b [32]byte
	bp := len(b)
	for ; u > 0 || wid > 0; u /= 10 {
		bp--
		wid--
		b[bp] = byte(u%10) + '0'
	}
	*buf = append(*buf, b[bp:]...)
}

func (l *Logger) formatHeader(buf *[]byte, t time.Time, file string, line int) {
	*buf = append(*buf, l.prefix...)
	if l.flag&(Ldate|Ltime|Lmicroseconds) != 0 {
		if l.flag&Ldate != 0 {
			year, month, day := t.Date()
			itoa(buf, year, 4)
			*buf = append(*buf, '-')
			itoa(buf, int(month), 2)
			*buf = append(*buf, '-')
			itoa(buf, day, 2)
			*buf = append(*buf, ' ')
		}
		if l.flag&(Ltime|Lmicroseconds) != 0 {
			hour, min, sec := t.Clock()
			itoa(buf, hour, 2)
			*buf = append(*buf, ':')
			itoa(buf, min, 2)
			*buf = append(*buf, ':')
			itoa(buf, sec, 2)
			if l.flag&Lmicroseconds != 0 {
				*buf = append(*buf, ',')
				itoa(buf, t.Nanosecond()/1e6, 3)
			}
			*buf = append(*buf, ' ')
		}
	}
	if l.flag&(Lshortfile|Llongfile) != 0 {
		if l.flag&Lshortfile != 0 {
			short := file
			for i := len(file) - 1; i > 0; i-- {
				if file[i] == '/' {
					short = file[i+1:]
					break
				}
			}
			file = short
		}
		*buf = append(*buf, file...)
		*buf = append(*buf, ':')
		itoa(buf, line, -1)
		*buf = append(*buf, ": "...)
	}
}

// Output writes the output for a logging event.  The string s contains
// the text to print after the prefix specified by the flags of the
// logger.  A newline is appended if the last character of s is not
// already a newline.  Calldepth is used to recover the PC and is
// provided for generality, although at the moment on all pre-defined
// paths it will be 2.
func (l *Logger) Output(calldepth int, s string) error {
	now := time.Now() // get this early.
	var file string
	var line int
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.flag&(Lshortfile|Llongfile) != 0 {
		// release lock while getting caller info - it's expensive.
		l.mu.Unlock()
		var ok bool
		_, file, line, ok = runtime.Caller(calldepth)
		if !ok {
			file = "???"
			line = 0
		}
		l.mu.Lock()
	}
	l.buf = l.buf[:0]
	l.formatHeader(&l.buf, now, file, line)
	l.buf = append(l.buf, s...)
	if len(s) > 0 && s[len(s)-1] != '\n' {
		l.buf = append(l.buf, '\n')
	}
	_, err := l.out.Write(l.buf)
	return err
}

// Printf calls l.Output to print to the logger.
// Arguments are handled in the manner of fmt.Printf.
func (l *Logger) Printf(format string, v ...interface{}) {
	l.Output(2, fmt.Sprintf(format, v...))
}

// Print calls l.Output to print to the logger.
// Arguments are handled in the manner of fmt.Print.
func (l *Logger) Print(v ...interface{}) { l.Output(2, fmt.Sprint(v...)) }

// Println calls l.Output to print to the logger.
// Arguments are handled in the manner of fmt.Println.
func (l *Logger) Println(v ...interface{}) { l.Output(2, fmt.Sprintln(v...)) }

// Fatal is equivalent to l.Print() followed by a call to os.Exit(1).
func (l *Logger) Fatal(v ...interface{}) {
	l.Output(2, fmt.Sprint(v...))
	os.Exit(1)
}

// Fatalf is equivalent to l.Printf() followed by a call to os.Exit(1).
func (l *Logger) Fatalf(format string, v ...interface{}) {
	l.Output(2, fmt.Sprintf(format, v...))
	os.Exit(1)
}

// Fatalln is equivalent to l.Println() followed by a call to os.Exit(1).
func (l *Logger) Fatalln(v ...interface{}) {
	l.Output(2, fmt.Sprintln(v...))
	os.Exit(1)
}

// Panic is equivalent to l.Print() followed by a call to panic().
func (l *Logger) Panic(v ...interface{}) {
	s := fmt.Sprint(v...)
	l.Output(2, s)
	panic(s)
}

// Panicf is equivalent to l.Printf() followed by a call to panic().
func (l *Logger) Panicf(format string, v ...interface{}) {
	s := fmt.Sprintf(format, v...)
	l.Output(2, s)
	panic(s)
}

// Panicln is equivalent to l.Println() followed by a call to panic().
func (l *Logger) Panicln(v ...interface{}) {
	s := fmt.Sprintln(v...)
	l.Output(2, s)
	panic(s)
}

// Flags returns the output flags for the logger.
func (l *Logger) Flags() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.flag
}

// SetFlags sets the output flags for the logger.
func (l *Logger) SetFlags(flag int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.flag = flag
}

// Prefix returns the output prefix for the logger.
func (l *Logger) Prefix() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.prefix
}

// SetPrefix sets the output prefix for the logger.
func (l *Logger) SetPrefix(prefix string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prefix = prefix
}

// SetOutput re-sets the output destination - by JFS team
func (l *Logger) SetOutput(w io.WriteCloser) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.out != nil {
		l.out.Close()
	}
	l.out = w

}

// SetOutput sets the output destination for the standard logger.
func SetOutput(w io.WriteCloser) {
	std.mu.Lock()
	defer std.mu.Unlock()
	std.out.Close()
	std.out = w

}

// Flags returns the output flags for the standard logger.
func Flags() int {
	return std.Flags()
}

// SetFlags sets the output flags for the standard logger.
func SetFlags(flag int) {
	std.SetFlags(flag)
}

// Prefix returns the output prefix for the standard logger.
func Prefix() string {
	return std.Prefix()
}

// SetPrefix sets the output prefix for the standard logger.
func SetPrefix(prefix string) {
	std.SetPrefix(prefix)
}

// These functions write to the standard logger.

// Print calls Output to print to the standard logger.
// Arguments are handled in the manner of fmt.Print.
func Print(v ...interface{}) {
	std.Output(2, fmt.Sprint(v...))
}

// Printf calls Output to print to the standard logger.
// Arguments are handled in the manner of fmt.Printf.
func Printf(format string, v ...interface{}) {
	std.Output(2, fmt.Sprintf(format, v...))
}

// Println calls Output to print to the standard logger.
// Arguments are handled in the manner of fmt.Println.
func Println(v ...interface{}) {
	std.Output(2, fmt.Sprintln(v...))
}

// Fatal is equivalent to Print() followed by a call to os.Exit(1).
func Fatal(v ...interface{}) {
	std.Output(2, fmt.Sprint(v...))
	os.Exit(1)
}

// Fatalf is equivalent to Printf() followed by a call to os.Exit(1).
func Fatalf(format string, v ...interface{}) {
	std.Output(2, fmt.Sprintf(format, v...))
	os.Exit(1)
}

// Fatalln is equivalent to Println() followed by a call to os.Exit(1).
func Fatalln(v ...interface{}) {
	std.Output(2, fmt.Sprintln(v...))
	os.Exit(1)
}

// Panic is equivalent to Print() followed by a call to panic().
func Panic(v ...interface{}) {
	s := fmt.Sprint(v...)
	std.Output(2, s)
	panic(s)
}

// Panicf is equivalent to Printf() followed by a call to panic().
func Panicf(format string, v ...interface{}) {
	s := fmt.Sprintf(format, v...)
	std.Output(2, s)
	panic(s)
}

// Panicln is equivalent to Println() followed by a call to panic().
func Panicln(v ...interface{}) {
	s := fmt.Sprintln(v...)
	std.Output(2, s)
	panic(s)
}

type Log struct {
	dir    string
	module string
	err    *Logger
	warn   *Logger
	debug  *Logger
	info   *Logger
	read   *Logger
	update *Logger

	level  int
	mesgCh chan string

	startTime time.Time
}

var levels = []string{
	"[DEBUG]",
	"[INFO.]",
	"[WARN.]",
	"[ERROR]",
	"[FATAL]",
	"[READ.]",
	"[UPDAT]",
}

const (
	DebugLevel  = 0
	InfoLevel   = 1
	WarnLevel   = 2
	ErrorLevel  = 3
	FatalLevel  = 4
	ReadLevel   = 5
	UpdateLevel = 6

	LogFileNameDateFormat = "2006-01-02"
)

var (
	ErrLogFileName    = "_err.log"
	WarnLogFileName   = "_warn.log"
	InfoLogFileName   = "_info.log"
	DebugLogFileName  = "_debug.log"
	ReadLogFileName   = "_read.log"
	UpdateLogFileName = "_write.log"
)

var glog *Log = nil

func GetLog() *Log {
	return glog
}

func NewLog(dir, module string, level int) (*Log, error) {
	glog = new(Log)
	glog.dir = dir
	glog.module = module
	fi, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, errors.New(dir + " is not a direnctoy")
	}

	err = glog.initLog(dir, module, level)
	if err != nil {
		return nil, err
	}
	glog.mesgCh = make(chan string, 102400)

	glog.startTime = time.Now()

	go glog.checkLogRotation(dir, module)
	go glog.GetMesg()

	return glog, nil
}

func (l *Log) initLog(logDir, module string, level int) error {
	const LogFileOpt = os.O_RDWR | os.O_CREATE | os.O_APPEND
	logOpt := LstdFlags | Lmicroseconds

	getNewLog := func(logFileName, errString string) (*Logger, error) {
		fp, e := os.OpenFile(logDir+"/"+module+logFileName, LogFileOpt, 0666)
		if e != nil {
			e = errors.New(errString)
		} else {
			newLog := New(fp, "", logOpt)
			return newLog, nil
		}
		return nil, e
	}
	var err error
	logHandles := [...]**Logger{&l.debug, &l.info, &l.warn, &l.err, &l.read, &l.update}
	logNames := [...]string{DebugLogFileName, InfoLogFileName, WarnLogFileName, ErrLogFileName, ReadLogFileName, UpdateLogFileName}
	logStr := [...]string{"Debug", "Info", "Warn", "Err", "Read", "Update"}
	for i := range logHandles {
		if *logHandles[i], err = getNewLog(logNames[i], logStr[i]+"LogFileOpenFailed"); err != nil {
			return err
		}
	}

	l.level = level

	return nil
}

func Debug(s string) {
	_, file, line, ok := runtime.Caller(2)
	if !ok {
		line = 0
	}
	short := file
	for i := len(file) - 1; i > 0; i-- {
		if file[i] == '/' {
			short = file[i+1:]
			break
		}
	}
	file = short
	fmt.Printf(file + ":" + strconv.Itoa(line) + " " + s)
}

func (l *Log) SetPrefix(s, level string) string {
	_, file, line, ok := runtime.Caller(2)
	if !ok {
		line = 0
	}
	short := file
	for i := len(file) - 1; i > 0; i-- {
		if file[i] == '/' {
			short = file[i+1:]
			break
		}
	}
	file = short

	return level + " " + file + ":" + strconv.Itoa(line) + ": " + s
}

func (l *Log) putMesg(mesg string, level int) {
	if level >= l.level {
		l.mesgCh <- mesg
	}
}

func (l *Log) LogWarn(v ...interface{}) {
	s := fmt.Sprintln(v...)
	s = l.SetPrefix(s, levels[2])
	l.putMesg(s, WarnLevel)
}

func (l *Log) LogInfo(v ...interface{}) {
	s := fmt.Sprintln(v...)
	s = l.SetPrefix(s, levels[1])
	l.putMesg(s, InfoLevel)
}

func (l *Log) LogError(v ...interface{}) {
	s := fmt.Sprintln(v...)
	s = l.SetPrefix(s, levels[3])
	l.putMesg(s, ErrorLevel)
}

func (l *Log) LogDebug(v ...interface{}) {
	s := fmt.Sprintln(v...)
	s = l.SetPrefix(s, levels[0])
	l.putMesg(s, DebugLevel)
}

func (l *Log) LogFatal(v ...interface{}) {
	s := fmt.Sprintln(v...)
	s = l.SetPrefix(s, levels[4])
	l.err.Output(2, s)
	os.Exit(1)
}

func (l *Log) LogRead(v ...interface{}) {
	s := fmt.Sprintln(v...)
	s = l.SetPrefix(s, levels[5])
	l.putMesg(s, ReadLevel)
}

func (l *Log) LogWrite(v ...interface{}) {
	s := fmt.Sprintln(v...)
	s = l.SetPrefix(s, levels[6])
	l.putMesg(s, UpdateLevel)
}

func (l *Log) GetMesg() {
	for {
		mesg := <-l.mesgCh
		switch mesg[1] {
		case 'W':
			l.warn.Print(mesg)
		case 'I':
			l.info.Print(mesg)
		case 'D':
			l.debug.Print(mesg)
		case 'E':
			l.err.Print(mesg)
		case 'R':
			l.read.Print(mesg)
		case 'U':
			l.update.Print(mesg)
		}
	}
}

func (l *Log) checkLogRotation(logDir, module string) {
	const LogFileOpt = os.O_RDWR | os.O_CREATE | os.O_APPEND
	for {
		yesterday := time.Now().AddDate(0, 0, -1)
		_, err := os.Stat(logDir + "/" + module + ErrLogFileName + "." + yesterday.Format(LogFileNameDateFormat))
		if err == nil || time.Now().Day() == l.startTime.Day() {
			time.Sleep(time.Second * 600)
			continue
		}

		setLogRotation := func(logFileName string, setLog *Logger) error {
			logFilePath := logDir + "/" + module + logFileName
			err := os.Rename(logFilePath, logFilePath+"."+yesterday.Format(LogFileNameDateFormat))
			if err != nil {
				return err
			}
			fp, err := os.OpenFile(logFilePath, LogFileOpt, 0666)
			if err != nil {
				return err
			}

			setLog.SetOutput(fp)

			return err
		}

		//rotate the log files
		setLogRotation(DebugLogFileName, l.debug)
		setLogRotation(InfoLogFileName, l.info)
		setLogRotation(WarnLogFileName, l.warn)
		setLogRotation(ErrLogFileName, l.err)
		setLogRotation(ReadLogFileName, l.read)
		setLogRotation(UpdateLogFileName, l.update)
	}
}
