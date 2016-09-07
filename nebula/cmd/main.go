package main

import (
	"flag"
	"net/http"

	. "git-pd.megvii-inc.com/zhangjian/manor/manor"
	log "github.com/Sirupsen/logrus"
	"github.com/julienschmidt/httprouter"
	"gopkg.in/thinxer/semikami.v2"
)

var (
	flagPort         = flag.String("port", ":6320", "Bind address")
	flagBindHttpsCrt = flag.String("https.crt", "./server.crt", "https server certificate")
	flagBindHttpsKey = flag.String("https.key", "./server.key", "https server key ")
)

func main() {
	flag.Parse()

	go InitFileServer()

	router := httprouter.New()
	k := kami.New(router)

	k.Get("/manor", ManorDemo)
	k.Post("/manor/update", Update)
	k.Post("/manor/delete", Delete)
	k.Get("/manor/updatehost", UpdateHost)
	k.Post("/manor/deletehost", DeleteHost)
	k.Get("/manor/configs", GetConfigs)
	k.Get("/manor/hostconfig", GetHostConfig)
	k.Get("/manor/hostslist", GetHostsList)
	k.Get("/manor/hoststatus", GetHostStatus)
	k.Get("/manor/modulestatus", GetHostsModuleStatus)
	k.Get("/manor/allstatus", GetAllStatus)
	k.Get("/manor/lateststatus", GetLatestStatus)
	k.Post("/manor/start", StartService)
	k.Post("/manor/stop", StopService)
	k.Post("/manor/report", Report)

	log.Infof("Listening at: %v", *flagPort)
	http.Handle("/", k)
	//	http.ListenAndServe(*flagPort, k)
	panic(http.ListenAndServeTLS(*flagPort, *flagBindHttpsCrt, *flagBindHttpsKey, http.DefaultServeMux))
}
