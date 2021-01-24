package robot

import (
	"fmt"

	"github.com/RyanDevlin/planet-pulse/pkg/ftpmachine"
)

func planetPulse() {
	server := ftpmachine.FTPServer{
		Hostname:      "aftp.cmdl.noaa.gov",
		Username:      "anonymous",
		Password:      "anonymous",
		ConnectionID:  0,
		Connections:   make(chan int32, 5), // This value is unique to the NOAA FTP server
		HaltSearch:    make(chan bool),
		CancelPending: make(chan bool),
	}

	ftpmachine := ftpmachine.FTPMachine{
		Server: server,
		Cache:  ftpmachine.FTPCache{},
	}

	data, err := ftpmachine.Server.Get("co2_weekly_mlo.txt", "/products/")
	if err != nil {
		fmt.Println(err)
	}

	fmt.Println(string(data[0]))
}
