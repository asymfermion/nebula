package manor

import (
	"flag"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	. "git-pd.megvii-inc.com/zhangjian/manor/interface"
	log "github.com/Sirupsen/logrus"
)

var (
	mux            map[string]func(http.ResponseWriter, *http.Request)
	flagFileServer = flag.String("fileServer", ":9090", "fileserver port")
	flagFileUrl    = flag.String("fileUrl", "http://10.101.4.66:9090", "fileserver download url")
)

type Myhandler struct{}
type home struct {
	Title string
}

const (
	UploadDir = "./upload/"
)

func InitFileServer() {
	flag.Parse()
	go cleanFiles()
	log.Infof("start fileServer")
	server := http.Server{
		Addr:        *flagFileServer,
		Handler:     &Myhandler{},
		ReadTimeout: 300 * time.Second,
	}
	mux = make(map[string]func(http.ResponseWriter, *http.Request))
	mux["/upload"] = upload
	mux["/file"] = StaticServer
	server.ListenAndServe()
}

func (*Myhandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h, ok := mux[r.URL.String()]; ok {
		h(w, r)
		return
	}
	http.StripPrefix("/", http.FileServer(http.Dir("./upload/"))).ServeHTTP(w, r)
}

func upload(w http.ResponseWriter, r *http.Request) {
	r.ParseMultipartForm(32 << 20)
	file, handler, err := r.FormFile("uploadfile")
	if err != nil {
		log.Errorf("upload fail: %v", err)
		WriteJSON(w, http.StatusOK, M{"status": "fail", "msg": err.Error()})
		return
	}
	fileext := filepath.Ext(handler.Filename)
	if check(fileext) == false {
		log.Errorf("upload fail: %v", "not allowed file type")
		WriteJSON(w, http.StatusOK, M{"status": "fail", "msg": "not allowed file type"})
		return
	}
	filename := handler.Filename
	f, _ := os.OpenFile(UploadDir+filename, os.O_CREATE|os.O_WRONLY, 0660)
	_, err = io.Copy(f, file)
	if err != nil {
		log.Errorf("upload fail: %v", err)
		WriteJSON(w, http.StatusOK, M{"status": "fail", "msg": err.Error()})
		return
	}
	fileUrl := *flagFileUrl + "/" + filename
	log.Infof("upload success: %s", filename)
	WriteJSON(w, http.StatusOK, M{"status": "success", "msg": fileUrl})
}

func StaticServer(w http.ResponseWriter, r *http.Request) {
	http.StripPrefix("/file", http.FileServer(http.Dir("./upload/"))).ServeHTTP(w, r)
}

func check(name string) bool {
	ext := []string{".exe", ".js", ".png"}

	for _, v := range ext {
		if v == name {
			return false
		}
	}
	return true
}

func cleanFiles() {
	for {
		err := filepath.Walk(UploadDir, func(path string, f os.FileInfo, err error) error {
			if f == nil {
				return err
			}
			if f.IsDir() {
				return nil
			}
			isExpired := int(time.Now().Sub(f.ModTime()).Hours()) > 7*24
			if isExpired {
				log.Infof("delete expired file: %s", f.Name())
				os.Remove(path)
			}
			return nil
		})
		if err != nil {
			log.Infof("filepath.Walk() returned %v\n", err)
		}
		time.Sleep(time.Hour)
	}
}
