package main

import (
	"rosman/lib/mikrotik"
	"time"
)

func main() {
	for _, host := range mikrotik.Hosts {
		go host.Run()
	}
	time.Sleep(time.Duration(1<<63 - 1))
}
