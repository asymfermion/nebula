package manor

import (
	"errors"
	"fmt"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/satori/go.uuid"
)

type LogEntry struct {
	f        *os.File
	st       time.Time
	uuid     string
	mode     string
	fn       string
	maxnum   int
	maxsize  string
	maxbyte  int
	mu       sync.Mutex
	interval int
}

type DirInfo struct {
	uuid string
	dir  string
	fl   []string
	fi   []os.FileInfo
	size int64
}

type Logm struct {
	data map[string]*LogEntry
}

type FileInfoSlice []os.FileInfo

func (fis FileInfoSlice) Len() int {
	return len(fis)
}

func (fis FileInfoSlice) Swap(i, j int) {
	fis[i], fis[j] = fis[j], fis[i]
}

func (fis FileInfoSlice) Less(i, j int) bool {
	ii := fis[i]
	jj := fis[j]
	return ii.Name() < jj.Name()
}

func (l *LogEntry) getLastTs() string {
	var name string
	t := time.Now()
	tt := t.Add(-1 * time.Duration(l.interval) * time.Minute)
	format := "%d%02d%02d_%02d"
	switch l.mode {
	case "minute":
		format = "%d%02d%02d_%02d%02d"
		name = fmt.Sprintf(format, tt.Year(), tt.Month(), tt.Day(), tt.Hour(), tt.Minute())
	case "day":
		format = "%d%02d%02d"
		name = fmt.Sprintf(format, tt.Year(), tt.Month(), tt.Day())
	default:
		name = fmt.Sprintf(format, tt.Year(), tt.Month(), tt.Day(), tt.Hour())
	}
	return name
}

func (l *LogEntry) doLogRotate() error {
	ts := l.getLastTs()
	f := l.fn + "." + ts
	l.mu.Lock()
	defer l.mu.Unlock()
	log.Infof("Begin to close: %s", l.fn)
	l.f.Close()
	err := os.Rename(l.fn, f)
	log.Infoln("Rotate", l.fn, "=>", f)
	if err != nil {
		return err
	}
	log.Infof("Open new file: %s", l.fn)
	ff, err := os.OpenFile(l.fn, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Errorf("Open new file %s err=%v", l.fn, err)
		return err
	}
	l.f = ff
	return nil
}

func (l *LogEntry) WriteLog(p []byte) (n int, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.f.Write(p)
}

func (l *LogEntry) maxSizeToBytes(s string) int {
	re := regexp.MustCompile(`([0-9]+)([bkmg])`)
	parts := re.FindStringSubmatch(s)
	if len(parts) != 3 {
		return 0
	}
	i := 1
	switch parts[2] {
	case "g":
		i = 1024 * 1024 * 1024
	case "m":
		i = 1024 * 1024
	case "k":
		i = 1024
	default:
	}
	b, _ := strconv.Atoi(parts[1])
	log.Debugln("size to byte", s, b*i)
	return b * i
}

func (lm *Logm) clearDir(di *DirInfo, l *LogEntry) error {
	log.Infoln("LOGM|begin clearDir: ", di.dir)
	i := 0
	fi := di.fi
	length := len(fi)
	for l.maxnum >= 0 && (length-i) > l.maxnum {
		fn := path.Join(di.dir, fi[i].Name())
		err := os.Remove(fn)
		if err != nil {
			log.Warnln("Delete log file", fn, "failed")
		} else {
			log.Infoln("clear log", fn)
			di.size = di.size - fi[i].Size()
			fi[i] = nil
		}
		i++
		log.Infoln("Now num", length-i, "maxnum", l.maxnum)
	}
	i = 0
	for l.maxbyte >= 0 && int(di.size) > l.maxbyte {
		if fi[i] == nil {
			continue
		}
		fn := path.Join(di.dir, fi[i].Name())
		err := os.Remove(fn)
		if err != nil {
			log.Warnln("Delte log file", fn, "failed")
		} else {
			log.Infoln("clear log", fn)
			di.size = di.size - fi[i].Size()
			fi[i] = nil
		}
		i++
		log.Infoln("Now size", di.size, "maxsize", l.maxbyte)
	}
	log.Infoln("LOGM|end of clearDir: ", di.dir)
	return nil
}

func (lm *Logm) loadDirs() map[string]*DirInfo {
	data := make(map[string]*DirInfo)
	for uuid, v := range lm.data {
		var total int64
		dir := path.Dir(v.fn)
		base := path.Base(v.fn)
		var di = DirInfo{
			uuid: uuid,
			dir:  dir,
		}
		f, err := os.OpenFile(dir, os.O_RDONLY, 0644)
		if err != nil {
			log.Warnln("Open dir fail", err.Error())
			continue
		}
		fi, err := f.Readdir(0)
		var fli FileInfoSlice
		fli = make([]os.FileInfo, 0)
		if err != nil {
			log.Warnln("Read dir fail", err.Error())
			continue
		}
		for _, i := range fi {
			name := i.Name()
			if i.IsDir() {
				continue
			}
			if strings.HasPrefix(name, base) && name != base {
				total += i.Size()
				fli = append(fli, i)
			}
		}
		di.size = total
		sort.Sort(fli)
		di.fi = fli
		data[uuid] = &di
	}
	return data
}

func (lm *Logm) Clear() {
	for {
		log.Infoln("LOGM|begin clear")
		di := lm.loadDirs()
		for uuid, v := range lm.data {
			if info, ok := di[uuid]; ok {
				err := lm.clearDir(info, v)
				if err != nil {
					log.Warnln("Clear dir", info.dir, " error", err.Error())
				}
			}
		}
		log.Infoln("LOGM|end of clear")
		<-time.After(1 * time.Minute)
	}
}

func (lm *Logm) getNewEntry(fn, mode, maxsize string, maxnum int) (*LogEntry, error) {
	f, err := os.OpenFile(fn, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}
	st := time.Now()
	//in minutes
	interval := 60
	switch mode {
	case "minute":
		interval = 1
	case "day":
		interval = 1440
	default:
	}
	l := &LogEntry{
		f:        f,
		st:       st,
		uuid:     uuid.NewV4().String(),
		mode:     mode,
		fn:       fn,
		interval: interval,
		maxsize:  maxsize,
		maxnum:   maxnum,
	}
	l.maxbyte = l.maxSizeToBytes(maxsize)

	//in seconds
	diff := 60*int64(interval) - st.Unix()%(60*int64(interval))
	log.Infoln(fn, "diff", diff)
	go func() {
		log.Infoln("Wait", diff, "seconds")
		<-time.After(time.Duration(diff) * time.Second)
		for {
			err := l.doLogRotate()
			if err != nil {
				log.Warnln("Do log rotate fail", err.Error())
			}
			<-time.After(time.Duration(interval) * time.Minute)
		}
	}()
	return l, nil
}

func (lm *Logm) Register(fn, mode, maxsize string, maxnum int) string {
	for _, v := range lm.data {
		if v.fn == fn {
			return v.uuid
		}
	}
	v, err := lm.getNewEntry(fn, strings.ToLower(mode), maxsize, maxnum)
	if err != nil {
		log.Warnln("New logm fail", err.Error())
	}
	lm.data[v.uuid] = v
	return v.uuid
}

func (lm *Logm) Write(uuid string, p []byte) (n int, err error) {
	var entry *LogEntry
	entry = nil
	if lm.data != nil {
		if l, ok := lm.data[uuid]; ok {
			entry = l
		}
	}
	if entry == nil {
		return 0, errors.New("No log file pointer")
	}
	return entry.WriteLog(p)
}

func (lm *Logm) Close(uuid string) {
	if v, ok := lm.data[uuid]; ok {
		v.f.Close()
		delete(lm.data, uuid)
	}
}

func (lm *Logm) CloseAll() {
	for k, v := range lm.data {
		v.f.Close()
		delete(lm.data, k)
	}
}

func (lm *Logm) Bg() {
	go lm.Clear()
}

func NewLogm() *Logm {
	lm := Logm{
		data: make(map[string]*LogEntry),
	}
	lm.Bg()
	return &lm
}

/*
func main() {
	lm := NewLogm()
	u1 := lm.Register("/tmp/log1", "a")
	u2 := lm.Register("/tmp/log1", "b")
	u3 := lm.Register("/tmp/log2", "c")
	fmt.Println(u1, u2, u3)
	for i := 0; i < 100; i++ {
		f := lm.GetFile(u3)
		f.Write([]byte(strconv.Itoa(i)))
		f.Write([]byte{'\n'})
		time.Sleep(1 * time.Second)
	}

}
*/
