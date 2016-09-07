package main

import (
	"flag"
	"os"
	"os/signal"
	"time"

	log "github.com/Sirupsen/logrus"
	"golang.org/x/sys/unix"

	. "git-pd.megvii-inc.com/zhangjian/manor/rancher"
)

var (
	flagUpdateURL      = flag.String("updateurl", "https://localhost:6320/manor/updatehost", "Update URL")
	flagReportURL      = flag.String("reporturl", "https://localhost:6320/manor/report", "Report URL")
	flagUpdateInterval = flag.Duration("interval", 5*time.Second, "Interval for checking updates")
	flagRepoPath       = flag.String("repo", "repo", "Repo path")
	flagReport         = flag.Bool("report", true, "Report machine info")
)

func DoUpdate(repo, updateurl, reporturl string) error {
	updater, err := NewUpdater(repo, updateurl, reporturl)
	if err != nil {
		log.Errorf("MAIN|New updater raise error: %v", err.Error())
		return err
	}
	sig := make(chan os.Signal)
	signal.Notify(sig, unix.SIGTERM, unix.SIGINT)
	log.Infof("MAIN|Updater inited")
	go func() {
		<-sig
		log.Infof("MAIN|Updater exiting...")
		updater.Stop()
		os.Exit(0)
	}()

	for {
		log.Infof("MAIN|start a update interval, process nums: %d", updater.GetProcessCount())
		if err := updater.TryUpdate(); err != nil {
			log.Errorf("MAIN|Error try update: %v", err.Error())
		}
		if *flagReport {
			log.Infof("MAIN|post report information")
			if err := updater.ReportInfo(); err != nil {
				log.Errorf("MAIN|Error postinfo: %v", err.Error())
			}
		}
		<-time.After(*flagUpdateInterval)
	}
	return nil
}

func main() {
	flag.Parse()

	cwd, err := os.Getwd()
	check(err)
	check(os.Setenv("HOME", cwd))

	DoUpdate(*flagRepoPath, *flagUpdateURL, *flagReportURL)
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}
