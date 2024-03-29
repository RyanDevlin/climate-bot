package robot

import (
	"fmt"

	"github.com/RyanDevlin/planet-pulse/pkg/ftpmachine"
)

func PlanetPulse() {
	machine, err := ftpmachine.NewFTPMachine("aftp.cmdl.noaa.gov", 21, "anonymous", "anonymous", 5, false)
	data, err := machine.Server.Get("co2_weekly_mlo.txt", "/products/trends", 0)
	if err != nil {
		fmt.Println(err)
		return
	}
	/*
		ftpEntry, err := machine.Server.GetMeta("co2_weekly_mlo.txt", "products/trends/")
		if err != nil {
			fmt.Println(err)
			return
		}*/

	fmt.Println(string(data[0]))

}
