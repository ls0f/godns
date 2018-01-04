package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"runtime/pprof"
	"time"
)

var (
	logger  *GoDNSLogger
	GitTag  = "dev"
	Version = "0.1.1"
	Build   = "2017-01-01"
	version = flag.Bool("v", false, "version")
)

func main() {

	flag.Parse()
	if *version {
		fmt.Fprintf(os.Stdout, "GitTag: %s\n", GitTag)
		fmt.Fprintf(os.Stdout, "Version: %s\n", Version)
		fmt.Fprintf(os.Stdout, "Build: %s\n", Build)
		return
	}

	initLogger()

	server := &Server{
		host:     settings.Server.Host,
		port:     settings.Server.Port,
		rTimeout: 5 * time.Second,
		wTimeout: 5 * time.Second,
	}

	server.Run()

	logger.Info("godns %s start", settings.Version)

	if settings.Debug {
		go profileCPU()
		go profileMEM()
	}

	sig := make(chan os.Signal)
	signal.Notify(sig, os.Interrupt)

forever:
	for {
		select {
		case <-sig:
			logger.Info("signal received, stopping")
			break forever
		}
	}

}

func profileCPU() {
	f, err := os.Create("godns.cprof")
	if err != nil {
		logger.Error("%s", err)
		return
	}

	pprof.StartCPUProfile(f)
	time.AfterFunc(6*time.Minute, func() {
		pprof.StopCPUProfile()
		f.Close()

	})
}

func profileMEM() {
	f, err := os.Create("godns.mprof")
	if err != nil {
		logger.Error("%s", err)
		return
	}

	time.AfterFunc(5*time.Minute, func() {
		pprof.WriteHeapProfile(f)
		f.Close()
	})

}

func initLogger() {
	logger = NewLogger()

	if settings.Log.Stdout {
		logger.SetLogger("console", nil)
	}

	if settings.Log.File != "" {
		config := map[string]interface{}{"file": settings.Log.File}
		logger.SetLogger("file", config)
	}

	logger.SetLevel(settings.Log.LogLevel())
}

func init() {
	runtime.GOMAXPROCS(runtime.NumCPU())
}
