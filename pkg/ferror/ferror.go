package ferror

import (
	"log"
	"os"
	"runtime"
	"strings"
)

/***********************************************************************
This file provides fancy error handling. It is a wrapper around the
standard logging methods and adds features such as file names and line
numbers in error messages
***********************************************************************/

func ErrorLog(err error) (b bool) {
	if err != nil {
		_, fn, line, _ := runtime.Caller(1)
		slice := strings.Split(fn, "/")
		log.Printf("[%v %s:%d]: Error - %v", os.Getppid(), slice[len(slice)-1], line, err)
		b = true
	}
	return
}

func InfoLog(msg string) {
	_, fn, line, _ := runtime.Caller(1)
	slice := strings.Split(fn, "/")
	log.Printf("[%v %s:%d]: Info - %v", os.Getppid(), slice[len(slice)-1], line, msg)
	return
}
