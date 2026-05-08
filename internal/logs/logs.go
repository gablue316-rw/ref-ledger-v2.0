package logs

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type LogDescriptor struct {
	Directory    string
	Filename     string
	FullPath     string
	Mode         string
	Fd           *os.File
	Status       string
	BytesWritten int
	Error        error
}

func (l *LogDescriptor) Open(dir, name, mode string) {

	l.Directory = dir
	l.Filename = name
	l.Mode = mode
	l.Error = nil

	l.FullPath = filepath.Join(l.Directory, l.Filename)

	flag := os.O_WRONLY | os.O_CREATE

	switch mode {
	case "append":
		flag |= os.O_APPEND
	case "truncate":
		flag |= os.O_TRUNC
	}

	l.Error = os.MkdirAll(l.Directory, os.ModePerm)
	if l.Error != nil {
		return
	}

	l.Fd, l.Error = os.OpenFile(l.FullPath, flag, 0644)
	if l.Error != nil {
		fmt.Println(l.Error)
	}
}

func (l *LogDescriptor) Close() {

	l.Error = nil
	l.Error = l.Fd.Close()
}

func (l *LogDescriptor) Write(text string) {

	l.Error = nil

	timeStamp := time.Now().Format("2006-01-02 15:04:05")

	l.BytesWritten, l.Error = l.Fd.WriteString(timeStamp + ": " + text + "\n")
	if l.Error != nil {
		return
	}

}
