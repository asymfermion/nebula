package rancher

import (
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"time"

	. "git-pd.megvii-inc.com/zhangjian/manor"
	. "git-pd.megvii-inc.com/zhangjian/manor/interface"
	log "github.com/Sirupsen/logrus"
	"log/syslog"
)

type Updater struct {
	lr         *LocalRepo
	supervisor *Supervisor
	client     *http.Client
	update     string
	report     string
	logWriter  *syslog.Writer
	lm         *Logm
	StatusMap  map[string]string
}

type LogWriter struct {
	writer *syslog.Writer
	level  log.Level
	tag    string

	root string
	last string
	t    string
	uuid string
	lm   *Logm
}

var CurrentRepos ServiceMap

func (w LogWriter) Write(p []byte) (n int, err error) {
	str := string(p)
	msgList := strings.Split(str, "\n")
	if w.writer != nil {
		for _, msg := range msgList {
			if msg == "" {
				continue
			}
			level := guessLevel(msg, w.level)
			switch level {
			case log.InfoLevel, log.DebugLevel:
				if code, err := w.writer.Write([]byte(msg)); err != nil {
					return code, err
				}
			default:
				if code, err := w.writer.Write([]byte(msg)); err != nil {
					return code, err
				}
			}
		}
	}
	n, err = w.lm.Write(w.uuid, p)
	if err != nil {
		log.Warnln("LOGGER|logging fail ", err.Error(), str)
	} else {
		log.Debugln("LOGGER|write ", string(p), "with", n, "bytes")
	}
	return len(p), nil
}

func (w LogWriter) Close() {
	if w.writer != nil {
		w.writer.Close()
	}
	if w.lm != nil {
		w.lm.CloseAll()
	}
}

func guessLevel(s string, defaultLevel log.Level) log.Level {
	items := strings.Split(s, " ")
	for _, item := range items {
		if strings.HasPrefix(item, "level=") {
			levelStr := strings.Split(item, "=")[1]
			level, err := log.ParseLevel(levelStr)
			if err == nil {
				return level
			}
			break
		}
	}
	return defaultLevel
}

func getLogmUuid(root, tag, t string, lm *Logm) string {
	fn := root + "/logs/" + tag + "." + t
	return lm.Register(fn, "hour", "2g", 50)
}

func getLogWriter(w *syslog.Writer, defaultLevel log.Level, lm *Logm, tag, root, t string) io.Writer {
	uuid := getLogmUuid(root, tag, t, lm)
	return LogWriter{
		level:  defaultLevel,
		writer: w,
		tag:    tag,
		root:   root,
		t:      t,
		uuid:   uuid,
		lm:     lm,
	}
}

func NewRepo(root string) (*LocalRepo, error) {
	err := os.MkdirAll(root, os.ModePerm)
	if err != nil {
		return nil, err
	}
	return &LocalRepo{Root: root, Cache: ServiceMap{}}, nil
}

func NewUpdater(repo, updateurl, reporturl string) (*Updater, error) {
	lr, err := NewRepo(repo)
	if err != nil {
		return nil, err
	}
	client := &http.Client{
		Transport: &http.Transport{
			DisableKeepAlives:   true,
			MaxIdleConnsPerHost: 1024,
			TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
		},
		Timeout: time.Duration(60) * time.Second,
	}
	w, err := syslog.New(syslog.LOG_INFO, "rancher")
	if err != nil {
		log.Errorf("Rancher| new syslog error: %v", err)
		return nil, err
	}
	lm := NewLogm()
	u := &Updater{
		lr:         lr,
		supervisor: NewSupervisor(),
		client:     client,
		update:     updateurl,
		report:     reporturl,
		logWriter:  w,
		lm:         lm,
		StatusMap:  map[string]string{},
	}
	return u, nil
}

func (u *Updater) GetProcessCount() int {
	if u.supervisor != nil {
		return len(u.supervisor.Processes)	
	} else {
		log.Errorf("Rancher|err nil supervisor pointer")
		return 0
	}
}

func (u *Updater) execute(dir, command string) error {
	if command == "" {
		return nil
	}
	filename := dir + "/" + time.Now().Format("2006-01-02-15:04:05") + ".sh"
	file, err := os.OpenFile(filename, os.O_WRONLY|os.O_TRUNC|os.O_CREATE, os.ModePerm)
	if err != nil {
		return err
	}
	buf := []byte(command)
	file.Write(buf)
	file.Close()
	cmd := exec.Command("bash", filename)
	cmd.Dir = dir
	if err := cmd.Start(); err != nil {
		log.Errorf("Rancher|err in command start")
		return err
	}
	if err := cmd.Wait(); err != nil {
		log.Errorf("Rancher|err in command wait")
		return err
	}
	os.Remove(filename)
	return nil
}

func (u *Updater) startone(m Manifest) error {
	log.Infof("Rancher|begin start %v", m.Name)
	runpath := path.Join(u.lr.Root, ".run")
	logpath := path.Join(u.lr.Root, "logs")
	os.MkdirAll(logpath, os.ModePerm)
	dir := path.Join(runpath, m.Name)
	os.RemoveAll(dir)

	if err := extractTo(dir, u.lr.Path(&m), m.Key); err != nil {
		log.Errorln("Rancher|err updating:", err)
		return err
	}

	if err := u.execute(dir, m.PreStart); err != nil {
		log.Errorf("Rancher|err update %v failed, prestart error: %v", m.Name, err)
		return err
	}
	log.Infof("Rancher|manifest pcnt: %v", m.Pcnt)
	if m.Pcnt > 1 {
		for i := 1; i <= m.Pcnt; i++ {
			pname := m.Name + "_" + strconv.Itoa(i)
			filename := pname + ".sh"
			file, err := os.OpenFile(dir+"/"+filename, os.O_WRONLY|os.O_TRUNC|os.O_CREATE, os.ModePerm)
			if err != nil {
				return nil
			}
			content := ""
			for _, e := range m.Env {
				e = strings.Trim(e, " ")
				if e != "" {
					content += "export " + e + "\n"
				}
			}
			content += "exec " +  m.Command
			for _, arg := range m.Args {
				if arg != "" {
					content += " '" + arg + "'"
				}
			}
			buf := []byte(content)
			file.Write(buf)
			file.Close()
			u.supervisor.Supervise(pname, func() *exec.Cmd {
				cmd := exec.Command("bash", "./"+filename)
				cmd.Dir = dir
				cmd.Stdout = getLogWriter(u.logWriter, log.InfoLevel, u.lm, pname, u.lr.Root, "stdout")
				cmd.Stderr = getLogWriter(u.logWriter, log.ErrorLevel, u.lm, pname, u.lr.Root, "stderr")
				return cmd
			}, &Options{KeepRunning: m.KeepRunning})
		}
	} else {
		u.supervisor.Supervise(m.Name, func() *exec.Cmd {
			filename := m.Name + ".sh"
			file, err := os.OpenFile(dir+"/"+filename, os.O_WRONLY|os.O_TRUNC|os.O_CREATE, os.ModePerm)
			if err != nil {
				return nil
			}
			content := ""
			for _, e := range m.Env {
				e = strings.Trim(e, " ")
				if e != "" {
					content += "export " + e + "\n"
				}
			}
			content += "exec " +  m.Command
			for _, arg := range m.Args {
				if arg != "" {
					content += " '" + arg + "'"
				}
			}
			buf := []byte(content)
			file.Write(buf)
			file.Close()
			cmd := exec.Command("bash", "./"+filename)
			cmd.Dir = dir
			cmd.Stdout = getLogWriter(u.logWriter, log.InfoLevel, u.lm, m.Name, u.lr.Root, "stdout")
			cmd.Stderr = getLogWriter(u.logWriter, log.ErrorLevel, u.lm, m.Name, u.lr.Root, "stderr")
			return cmd
		}, &Options{KeepRunning: m.KeepRunning})
	}

	if err := u.execute(dir, m.AfterStart); err != nil {
		log.Errorf("Rancher|err update %v failed, afterstart error: %v", m.Name, err)
		return err
	}
	return nil
}

func (u *Updater) TryUpdate() (err error) {
	defer func() {
		if err != nil {
			log.Errorln("Rancher|err updating:", err)
		} else if e := recover(); e != nil {
			log.Errorln("Rancher|panic updating:", e)
		}
	}()

	log.Infof("Rancher|begin TryUpdate")	
	var repos ServiceMap
	if err := fetchJSON(u.client, u.update, &repos); err != nil {
		return err
	}
	log.Infof("Rancher|fetch json success")
	CurrentRepos = repos

	for name, p := range CurrentRepos {
		origpkg, ok := u.lr.Cache[name]
		updated, err := u.lr.CheckUpdate(u.client, &p)
		if err != nil {
			log.Errorf("Rancher|err CheckUpdate %v error: %v", name, err)
			continue
		}

		// reload process
		if updated {
			log.Infof("Rancher| %v updated, begin update", name)
			u.StatusMap[name] = STATUS_UPDATING
			u.ReportUpdateInfo(p)
			if ok {
				log.Infof("Rancher| find %v in cache, Pcnt : %v, begin stop it", name, origpkg.Pcnt)
				u.supervisor.StopOne(origpkg)
			}
			if p.KeepRunning {
				if err := u.startone(p); err != nil {
					u.StatusMap[name] = STATUS_UPDATE_FAILED
					u.ReportUpdateInfo(p)
					u.supervisor.StopOne(p)
					log.Errorf("Rancher|err update %v error: %v", name, err)
					continue
				}
				u.StatusMap[name] = STATUS_RUNNING
				u.ReportUpdateInfo(p)
			} else {
				u.StatusMap[name] = STATUS_STOPPED
				u.ReportUpdateInfo(p)
			}
			log.Infof("Rancher|update %v success, status: %v", name, u.StatusMap[name])
		}
	}
	log.Infof("Rancher|end of TryUpdate")	

	return nil
}

func (u *Updater) Stop() {
	for _, m := range u.lr.Cache {
		u.supervisor.StopOne(m)
		u.StatusMap[m.Name] = STATUS_STOPPED
		u.ReportUpdateInfo(m)
	}
	u.logWriter.Close()
}

func fetchJSON(client *http.Client, url string, v interface{}) error {
	var reader io.Reader
	if strings.HasPrefix(url, "http:") || strings.HasPrefix(url, "https:") {
		addr, err := getAddr()
		if err != nil {
			return err
		}
		realurl := url + "?host=" + addr.IP
		log.Infoln("Rancher|fectching url ", realurl)
		req, err := http.NewRequest("GET", realurl, nil)
		if err != nil {
			return err
		}
		req.Header.Add("Accept-Encoding", "identity")
		req.Close = true
		resp, err := client.Do(req)
		if err != nil {
			log.Errorln("Rancher|err fetch http config fail", url, err.Error())
			return err
		}
		defer resp.Body.Close()
		reader = resp.Body
	} else {
		file, err := os.Open(url)
		log.Errorln("Rancher|err open file fail", url, err.Error())
		if err != nil {
			return err
		}
		defer file.Close()
		reader = file
	}
	return json.NewDecoder(reader).Decode(v)
}

func (u *Updater) ReportUpdateInfo(m Manifest) error {
	var data ReportData
	data.Token = os.Getenv("UPDATER_TOKEN")
	hostname, err := os.Hostname()
	if err != nil {
		log.Warnln("Rancher|err Get hostname failed: ", err)
	}
	data.Hostname = hostname
	addr, err := getAddr()
	if err != nil {
		log.Warnln("Rancher|err Get address failed: ", err)
	}
	data.Addr = addr
	var packageInfo PackageInfo
	packageInfo.Name = m.Name
	packageInfo.Version = m.Version
	packageInfo.Status = map[string]string{m.Name: u.StatusMap[m.Name]}
	data.Packages = append(data.Packages, packageInfo)
	postURL := strings.Split(u.report, "?")[0]
	_, err = PostJson(u.client, postURL, data)
	if err != nil {
		return err
	}
	return nil
}

func getAllProcessStatus(pcnt int, status map[string]string) string {
	runCnt := 0
	for _, stat := range status {
		if stat == STATUS_RUNNING {
			runCnt += 1
		}
	}
	if runCnt < pcnt || runCnt == 0 {
		return STATUS_NOALLRUNNING
	} else {
		return STATUS_RUNNING
	}
}

func (u *Updater) ReportInfo() error {
	var data ReportData
	data.Token = os.Getenv("UPDATER_TOKEN")
	hostname, err := os.Hostname()
	if err != nil {
		log.Warnln("Rancher|Get hostname failed: ", err)
	}
	data.Hostname = hostname
	addr, err := getAddr()
	if err != nil {
		log.Warnln("Rancher|Get address failed: ", err)
	}
	data.Addr = addr
	if u.lr.Cache != nil {
		for _, cli := range u.lr.Cache {
			var packageInfo PackageInfo
			packageInfo.Name = cli.Name
			packageInfo.Version = cli.Version
			if u.StatusMap[cli.Name] == STATUS_RUNNING {
				packageInfo.Status = u.supervisor.GetStatus(cli)
				if cli.Pcnt > 1 {
					packageInfo.Status[cli.Name] = getAllProcessStatus(cli.Pcnt, packageInfo.Status)
				}
			} else {
				packageInfo.Status = map[string]string{cli.Name: u.StatusMap[cli.Name]}
			}
			data.Packages = append(data.Packages, packageInfo)
		}
	}
	postURL := strings.Split(u.report, "?")[0]
	_, err = PostJson(u.client, postURL, data)
	if err != nil {
		return err
	}
	return nil
}

func getAddr() (Address, error) {
	var address Address
	interfaces, err := net.Interfaces()
	if err != nil {
		return address, err
	}
	for _, inter := range interfaces {
		if addrs, err := inter.Addrs(); err == nil {
			for _, addr := range addrs {
				if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
					if ipnet.IP.To4() != nil && inter.Name == "eth0" {
						return Address{
							IP:  ipnet.IP.String(),
							MAC: inter.HardwareAddr.String(),
						}, nil
					}
				}
			}
		} else {
			continue
		}
	}
	return address, nil
}
