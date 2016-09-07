package datainterface

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strings"

	log "github.com/Sirupsen/logrus"
)

type M map[string]interface{}

type Manifest struct {
	Name    string `json:"name"`
	URL     string `json:"url"`
	Key     []byte `json:"key"`
	Version int    `json:"version"`
	Sha256  string `json:"sha256"`

	PreStart   string   `json:"prestart"`
	AfterStart string   `json:"afterstart"`
	Command    string   `json:"command"`
	Args       []string `json:"args"`
	Env        []string `json:"env"`
	Pcnt       int      `json:"pcnt"`

	// Default policy is to restart on failures.
	KeepRunning bool `json:"keeprunning"`
}

type ServiceMap map[string]Manifest

type ReportData struct {
	Token    string        `json:"token"`
	Addr     Address       `json:"address"`
	Hostname string        `json:"hostname"`
	Packages []PackageInfo `json:"packages"`
}

type LocalRepo struct {
	Root  string
	Cache ServiceMap
}

type RepoData struct {
	Hosts   []string `json:"hosts"`
	Package Manifest `json:"service"`
}

type PackageInfo struct {
	Name    string            `json:"name"`
	Version int               `json:"version"`
	Status  map[string]string `json:"status"`
}

type Address struct {
	IP  string `json:"ip"`
	MAC string `json:"mac"`
}

const (
	STATUS_RUNNING       = "running"
	STATUS_NOALLRUNNING  = "not_all_running"
	STATUS_UPDATE_FAILED = "update_failed"
	STATUS_UPDATING      = "updating"
	STATUS_STOPPED       = "stopped"
	STATUS_NOT_INSTALLED = "not_installed"
)

func (m *Manifest) String() string {
	return fmt.Sprintf("%s[%d] %s", m.Name, m.Version, m.URL)
}

func (l *LocalRepo) CheckUpdate(client *http.Client, repo *Manifest) (updated bool, err error) {
	// check if packages updated
	if l.Cache != nil {
		if _, ok := l.Cache[repo.Name]; ok {
			localpkg := l.Cache[repo.Name]
			updated = localpkg.Version != repo.Version ||
				localpkg.Sha256 != repo.Sha256 ||
				localpkg.Command != repo.Command ||
				localpkg.PreStart != repo.PreStart ||
				localpkg.AfterStart != repo.AfterStart ||
				strings.Join(localpkg.Env, " ") != strings.Join(repo.Env, " ") ||
				strings.Join(localpkg.Args, " ") != strings.Join(repo.Args, " ") ||
				localpkg.Pcnt != repo.Pcnt ||
				localpkg.KeepRunning != repo.KeepRunning
		} else {
			updated = true
		}
	} else {
		updated = true
		l.Cache = ServiceMap{}
	}

	// download packages
	if updated {
		if err := l.Download(client, repo); err != nil {
			return false, err
		}
		// update local repo
		log.Infof("CheckUpdate |download success from %v", repo.URL)
		l.Cache[repo.Name] = *repo
	}

	return updated, nil
}

func (l *LocalRepo) Download(client *http.Client, m *Manifest) error {
	if err := l.Checksum(m); err == nil {
		return nil
	}

	pkgpath := l.Path(m)
	if err := os.MkdirAll(path.Dir(pkgpath), os.ModePerm); err != nil {
		return err
	}

	file, err := os.Create(pkgpath)
	if err != nil {
		return err
	}
	defer file.Close()

	req, err := http.NewRequest("GET", m.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Add("Accept-Encoding", "identity")
	req.Close = true
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if _, err = io.Copy(file, resp.Body); err != nil {
		return err
	}

	return l.Checksum(m)
}

func (l *LocalRepo) Checksum(m *Manifest) (err error) {
	pkgpath := l.Path(m)
	file, err := os.Open(pkgpath)
	if err != nil {
		return
	}
	defer file.Close()

	h := sha256.New()
	if _, err = io.Copy(h, file); err != nil {
		return
	}
	if hex.EncodeToString(h.Sum(nil)) != m.Sha256 {
		return errors.New("checksum mismatch: " + hex.EncodeToString(h.Sum(nil)))
	}
	return nil
}

func (l *LocalRepo) Path(m *Manifest) string {
	return path.Join(l.Root, m.Name)
}
