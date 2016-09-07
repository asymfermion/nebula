package manor

import (
	"encoding/json"
	"errors"
	"io/ioutil"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"sync"
	"time"

	. "git-pd.megvii-inc.com/zhangjian/manor/interface"
	log "github.com/Sirupsen/logrus"
	"golang.org/x/net/context"
	"golang.org/x/sys/unix"
)

type RancherMap map[string]*Services
type StringList struct {
	List   []string
	Status string
}

type Services struct {
	StatusMap  map[string]map[string]string `json:"statusmap"`
	StatusList map[string]*StringList       `json:"statuslist"`
	ServMap    ServiceMap                   `json:"services"`
	mutex      sync.Mutex
}

var (
	ranchermap = RancherMap{}
	host       string
	URLPattern = regexp.MustCompile(`^https?://`)
)

func (rm RancherMap) StoreConfig() error {
	configs := map[string]ServiceMap{}
	for k, v := range rm {
		configs[k] = v.ServMap
	}
	b, err := json.Marshal(configs)
	file, err := os.OpenFile("manor.json", os.O_WRONLY|os.O_TRUNC|os.O_CREATE, os.ModePerm)
	defer file.Close()
	if err != nil {
		return err
	}
	file.Write([]byte(b))
	return nil
}

func (sl *StringList) Clear() {
	sl = nil
}

func (sl *StringList) Append(s string) {
	sl.List = append(sl.List, s)
}

func (s *Services) UpdateStatusList(module, status string) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	idx := len(s.StatusList[module].List) - 1
	if idx >= 0 && status != s.StatusList[module].List[idx] {
		s.StatusList[module].Append(status)
	} else if idx == -1 {
		s.StatusList[module].Append(status)
	}
}

func (s *Services) GetLatestStatus(module string) string {
	var status string
	if statusList, ok := s.StatusList[module]; ok {
		for _, s := range statusList.List {
			status += s + "->"
		}
	}
	status = strings.Trim(status, "->")
	return status
}

func (s *Services) ClearStatusList() {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	for module, _ := range s.StatusList {
		status := s.GetLatestStatus(module)
		s.StatusList[module].Clear()
		s.StatusList[module] = &StringList{
			List:   []string{s.StatusMap[module][module]},
			Status: status,
		}
	}
}

func WriteJSON(w http.ResponseWriter, code int, v interface{}) error {
	w.Header().Set("Content-Type", "application/json;charset=utf-8")
	w.WriteHeader(code)
	return json.NewEncoder(w).Encode(v)
}

func WriteReply(w http.ResponseWriter, code int, reply []byte) error {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(code)
	cnt, err := w.Write(reply)
	if err != nil {
		return err
	}
	if cnt != len(reply) {
		log.Errorf("write reply incomplete")
		return errors.New("incompelete reply write")
	}
	return nil
}

func ManorDemo(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	WriteReply(w, http.StatusOK, []byte("welcome to manor!"))
}

func updateRepo(repo RepoData) {
	module := repo.Package.Name
	for _, v := range repo.Hosts {
		log.Infof("update %v module: %v, %v", v, module, repo.Package)
		if _, ok := ranchermap[v]; ok {
			log.Infof("Manor| exist host %v, update to ranchermap", module)
			ranchermap[v].ServMap[module] = repo.Package
			ranchermap[v].StatusMap[module] = map[string]string{module: STATUS_UPDATING}
			if _, ok := ranchermap[v].StatusList[module]; ok {
				ranchermap[v].StatusList[module].Append(STATUS_UPDATING)
			} else {
				ranchermap[v].StatusList[module] = &StringList{
					List:   []string{STATUS_UPDATING},
					Status: STATUS_UPDATING,
				}
			}
		} else {
			log.Infof("Manor| new host %s package %v, add to ranchermap", v, module)
			ranchermap[v] = &Services{
				StatusMap:  map[string]map[string]string{},
				StatusList: map[string]*StringList{},
				ServMap:    ServiceMap{},
			}
			ranchermap[v].StatusMap[module] = map[string]string{
				module: STATUS_UPDATING,
			}
			ranchermap[v].StatusList[module] = &StringList{
				List:   []string{STATUS_UPDATING},
				Status: STATUS_UPDATING,
			}
			ranchermap[v].ServMap[module] = repo.Package
		}
	}
}

func Update(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	var repo RepoData
	err := json.NewDecoder(r.Body).Decode(&repo)
	defer r.Body.Close()
	if err != nil {
		log.Errorf("Decode error: %v", err)
		WriteReply(w, http.StatusOK, []byte("request decode failed"))
	} else {
		updateRepo(repo)
		WriteReply(w, http.StatusOK, []byte("ok"))
	}
}

func deleteHostModule(host, module string) error {
	if _, ok := ranchermap[host]; ok {
		if serv, ok := ranchermap[host].ServMap[module]; ok {
			if serv.KeepRunning == true {
				return errors.New("service still running")
			}
			delete(ranchermap[host].ServMap, module)
		}
		if _, ok := ranchermap[host].StatusMap[module]; ok {
			delete(ranchermap[host].StatusMap, module)
		}
	}
	return nil
}

func Delete(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	host := r.FormValue("host")
	if host == "" {
		WriteReply(w, http.StatusOK, []byte("no host given"))
		return
	}
	module := r.FormValue("module")
	if module == "" {
		WriteReply(w, http.StatusOK, []byte("no module given"))
		return
	}
	hostsList := strings.Split(host, ",")
	resultMap := map[string]string{}
	for _, h := range hostsList {
		log.Infof("Manor| API Delete: delete host: %v, module: %v", host, module)
		err := deleteHostModule(h, module)
		if err != nil {
			resultMap[h] = err.Error()
			log.Errorf("Manor| API Delete: delete host: %s, module: %s, error: %v", h, module, err)
		} else {
			resultMap[h] = "ok"
		}
	}
	WriteJSON(w, http.StatusOK, resultMap)
}

func UpdateHost(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	host := r.FormValue("host")
	configs := ServiceMap{}
	if serv, ok := ranchermap[host]; ok {
		configs = serv.ServMap
	}
	WriteJSON(w, http.StatusOK, configs)
}

func detectAlive(servers ServiceMap) error {
	for k, v := range servers {
		if v.KeepRunning == true {
			return errors.New(k + " is still running")
		}
	}
	return nil
}

func DeleteHost(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	hosts := r.FormValue("host")
	if hosts == "" {
		WriteReply(w, http.StatusOK, []byte("no host given"))
		return
	}
	hostsList := strings.Split(hosts, ",")
	resultMap := map[string]string{}
	for _, host := range hostsList {
		if rancher, ok := ranchermap[host]; ok {
			log.Infof("Manor| API DeleteHost: delete %v", host)
			err := detectAlive(rancher.ServMap)
			if err != nil {
				resultMap[host] = err.Error()
				log.Errorf("Manor| API DeleteHost: delete host: %s, error: %v", host, err)
			} else {
				delete(ranchermap, host)
			}
		}
	}
	WriteJSON(w, http.StatusOK, resultMap)
}

func getConfigs() map[string]ServiceMap {
	configs := map[string]ServiceMap{}
	for k, v := range ranchermap {
		configs[k] = v.ServMap
	}
	return configs
}

func GetConfigs(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	configs := getConfigs()
	WriteJSON(w, http.StatusOK, configs)
}

func getHostConfig(host string) ServiceMap {
	if s, ok := ranchermap[host]; ok {
		return s.ServMap
	}
	return ServiceMap{}
}

func GetHostConfig(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	host := r.FormValue("host")
	if host == "" {
		WriteReply(w, http.StatusOK, []byte("no host given"))
		return
	}
	configs := getHostConfig(host)
	WriteJSON(w, http.StatusOK, configs)
}

func getHostsList() []string {
	var hosts []string
	for k, _ := range ranchermap {
		hosts = append(hosts, k)
	}
	return hosts
}

func GetHostsList(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	hostsList := getHostsList()
	WriteJSON(w, http.StatusOK, hostsList)
}

func getPackageInfo(host string, name string) PackageInfo {
	var pi PackageInfo
	pi.Name = name
	if _, ok := ranchermap[host].StatusMap[name]; ok {
		pi.Status = ranchermap[host].StatusMap[name]
	} else {
		pi.Status = map[string]string{}
		log.Infof("Manor| get host %v service %v status: name not in statusmap", host, name)
	}
	if len(pi.Status) == 0 {
		log.Infof("Manor| get host %v service %v status: status is nil", host, name)
		pi.Status = map[string]string{name: STATUS_NOT_INSTALLED}
	}
	return pi
}

func getHostStatus(host string) []PackageInfo {
	log.Infof("Manor| get %v service status", host)
	status := []PackageInfo{}
	for name, _ := range ranchermap[host].StatusMap {
		pi := getPackageInfo(host, name)
		status = append(status, pi)
	}
	return status
}

func GetHostStatus(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	host := r.FormValue("host")
	if host == "" {
		WriteReply(w, http.StatusOK, []byte("no host given"))
		return
	}
	status := getHostStatus(host)
	WriteJSON(w, http.StatusOK, status)
}

func getHostsModuleStatus(hostsList []string, module string) map[string]PackageInfo {
	log.Infof("Manor| get hosts %v service %v status", hostsList, module)
	status := map[string]PackageInfo{}
	for _, host := range hostsList {
		if serv, ok := ranchermap[host]; ok {
			if _, ok := serv.ServMap[module]; ok {
				status[host] = getPackageInfo(host, module)
			} else {
				status[host] = PackageInfo{module, -1, map[string]string{module: STATUS_NOT_INSTALLED}}
			}
		} else {
			status[host] = PackageInfo{module, -1, map[string]string{module: STATUS_NOT_INSTALLED}}
		}
	}
	return status
}

func GetHostsModuleStatus(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	host := r.FormValue("host")
	if host == "" {
		WriteReply(w, http.StatusOK, []byte("no host given"))
		return
	}
	module := r.FormValue("module")
	if module == "" {
		WriteReply(w, http.StatusOK, []byte("no module given"))
		return
	}
	hostsList := strings.Split(host, ",")
	status := getHostsModuleStatus(hostsList, module)
	WriteJSON(w, http.StatusOK, status)
}

func getAllStatus() map[string][]PackageInfo {
	status := map[string][]PackageInfo{}
	for host, _ := range ranchermap {
		status[host] = getHostStatus(host)
	}
	return status
}

func GetAllStatus(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	status := getAllStatus()
	WriteJSON(w, http.StatusOK, status)
}

func getLatestStatus(hosts, modules []string) map[string]map[string]string {
	status := map[string]map[string]string{}
	for _, host := range hosts {
		if services, ok := ranchermap[host]; ok {
			hostStatus := map[string]string{}
			for _, module := range modules {
				hostStatus[module] = services.StatusList[module].Status
			}
			status[host] = hostStatus
		}
	}
	return status
}

func GetLatestStatus(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	host := r.FormValue("host")
	if host == "" {
		WriteReply(w, http.StatusOK, []byte("no host given"))
		return
	}
	module := r.FormValue("module")
	if module == "" {
		WriteReply(w, http.StatusOK, []byte("no module given"))
		return
	}
	hostsList := strings.Split(host, ",")
	moduleList := strings.Split(module, ",")
	status := getLatestStatus(hostsList, moduleList)
	WriteJSON(w, http.StatusOK, status)
}

func updateModuleStatus(host, module string, status bool) {
	if tmpModule, ok := ranchermap[host].ServMap[module]; ok {
		tmpModule.KeepRunning = status
		ranchermap[host].ServMap[module] = tmpModule
	}
}

func startService(hosts, modules []string) {
	for _, host := range hosts {
		if _, ok := ranchermap[host]; ok {
			for _, module := range modules {
				log.Infof("Manor| start %v service %v", host, module)
				updateModuleStatus(host, module, true)
			}
		}
	}
}

func StartService(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	hosts := r.FormValue("host")
	if hosts == "" {
		WriteReply(w, http.StatusOK, []byte("no host given"))
		return
	}
	modules := r.FormValue("module")
	if modules == "" {
		WriteReply(w, http.StatusOK, []byte("no module given"))
		return
	}
	hostsList := strings.Split(hosts, ",")
	moduleList := strings.Split(modules, ",")
	startService(hostsList, moduleList)
	WriteReply(w, http.StatusOK, []byte("ok"))
}

func stopService(hosts, modules []string) {
	for _, host := range hosts {
		if _, ok := ranchermap[host]; ok {
			for _, module := range modules {
				log.Infof("Manor| stop %v service %v", host, module)
				updateModuleStatus(host, module, false)
			}
		}
	}
}

func StopService(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	hosts := r.FormValue("host")
	if hosts == "" {
		WriteReply(w, http.StatusOK, []byte("no host given"))
		return
	}
	modules := r.FormValue("module")
	if modules == "" {
		WriteReply(w, http.StatusOK, []byte("no module given"))
		return
	}
	hostsList := strings.Split(hosts, ",")
	moduleList := strings.Split(modules, ",")
	stopService(hostsList, moduleList)
	WriteReply(w, http.StatusOK, []byte("ok"))
}

func updateStatus(reports ReportData) {
	ip := reports.Addr.IP
	for _, p := range reports.Packages {
		if _, ok := ranchermap[ip]; ok {
			if _, ok := ranchermap[ip].StatusList[p.Name]; ok {
				ranchermap[ip].UpdateStatusList(p.Name, p.Status[p.Name])
			} else {
				ranchermap[ip].StatusList[p.Name] = &StringList{
					List: []string{},
				}
				ranchermap[ip].UpdateStatusList(p.Name, p.Status[p.Name])
			}
			ranchermap[ip].StatusMap[p.Name] = p.Status
		} else {
			ranchermap[ip] = &Services{
				StatusMap:  map[string]map[string]string{},
				StatusList: map[string]*StringList{},
				ServMap:    ServiceMap{},
			}
			ranchermap[ip].StatusList[p.Name] = &StringList{
				List: []string{},
			}
			ranchermap[ip].StatusMap[p.Name] = p.Status
			ranchermap[ip].UpdateStatusList(p.Name, p.Status[p.Name])
		}
	}
}

func Report(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	var reports ReportData
	err := json.NewDecoder(r.Body).Decode(&reports)
	defer r.Body.Close()
	if err != nil {
		log.Errorf("Manor| update request decode failed ")
		WriteReply(w, http.StatusOK, []byte("fail"))
	} else {
		updateStatus(reports)
		WriteReply(w, http.StatusOK, []byte("ok"))
	}
}

func loadConfig() error {
	fi, err := os.Open("manor.json")
	if err != nil {
		return err
	}
	defer fi.Close()
	fd, err := ioutil.ReadAll(fi)

	cfg := map[string]ServiceMap{}
	if err := json.Unmarshal(fd, &cfg); err != nil {
		log.Errorf("Manor| Unmarshal config error: ", err.Error())
		return err
	}
	for k, v := range cfg {
		if v != nil {
			ranchermap[k] = &Services{
				StatusMap:  map[string]map[string]string{},
				StatusList: map[string]*StringList{},
				ServMap:    v,
			}
			for name, _ := range v {
				ranchermap[k].StatusList[name] = &StringList{
					List:   []string{},
					Status: "",
				}
				ranchermap[k].StatusMap[name] = map[string]string{}
			}
		}
	}
	return nil
}

func clearStatusList() {
	for {
		time.Sleep(60 * time.Second)
		for host, _ := range ranchermap {
			ranchermap[host].ClearStatusList()
		}
	}
}

func init() {
	loadConfig()
	sig := make(chan os.Signal)
	signal.Notify(sig, unix.SIGTERM, unix.SIGINT)
	go func() {
		<-sig
		log.Infof("MAIN|manor exiting...")
		err := ranchermap.StoreConfig()
		if err != nil {
			log.Errorf("MAIN|manor store config fail: %v", err)
		}
		os.Exit(0)
	}()

	go clearStatusList()
}
