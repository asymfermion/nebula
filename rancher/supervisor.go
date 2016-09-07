package rancher

import (
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	. "git-pd.megvii-inc.com/zhangjian/manor/interface"
	log "github.com/Sirupsen/logrus"
)

type Options struct {
	// Do not restart the process even on failures.
	NoRestart bool
	// keep the process running regardless of the return code.
	KeepRunning bool
}

var defaultOptions = &Options{}

type Process struct {
	Status string
	Kill   func()
}

type Supervisor struct {
	Processes map[string]*Process

	mu sync.Mutex
}

func NewSupervisor() *Supervisor {
	return &Supervisor{
		Processes: make(map[string]*Process),
	}
}

func UpdataStatus(p *Process, status string) {
	p.Status = status
}

func (s *Supervisor) Supervise(name string, gen func() *exec.Cmd, options *Options) {
	if options == nil {
		options = defaultOptions
	}

	ch := make(chan struct{}, 1)

	s.mu.Lock()
	s.Processes[name] = &Process{
		Status: STATUS_UPDATING,
		Kill: func() {
			close(ch)
		},
	}
	s.mu.Unlock()

	go func() {
		var cmd *exec.Cmd
		go func(ccmd **exec.Cmd) {
			<-ch
			cmd := *ccmd
			if cmd != nil && cmd.Process != nil {
				err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				if err != nil {
					log.Errorf("SUPERVISOR|err failed to kill process: %v PID: %v", err, cmd.Process.Pid)
				}
			}
		}(&cmd)

	runloop:
		for {
			select {
			case <-ch:
				return
			default:
			}
			cmd = gen()
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			log.Infof("SUPERVISOR| %v starting %v in %v", name, strings.Join(cmd.Args, " "), cmd.Dir)
			if err := cmd.Start(); err != nil {
				log.Errorf("SUPERVISOR| %v failed to start: %v", name, err)
				if options.NoRestart {
					break runloop
				}
				if _, ok := s.Processes[name]; ok {
					UpdataStatus(s.Processes[name], STATUS_STOPPED)
				}
				time.Sleep(30 * time.Second)
				continue runloop
			}
			UpdataStatus(s.Processes[name], STATUS_RUNNING)
			log.Infof("SUPERVISOR| update %v status %v", name, s.Processes[name].Status)

			if err := cmd.Wait(); err != nil {
				log.Errorf("SUPERVISOR| %v exited: %v", name, err)
				if options.NoRestart {
					break runloop
				}
				if _, ok := s.Processes[name]; ok {
					UpdataStatus(s.Processes[name], STATUS_STOPPED)
				}
				time.Sleep(10 * time.Second)
				continue runloop
			}

			UpdataStatus(s.Processes[name], STATUS_STOPPED)
			log.Infof("SUPERVISOR| %v exited normally", name)

			if !options.KeepRunning {
				break runloop
			}
		}
	}()
}

func (s *Supervisor) StopOne(m Manifest) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if m.Pcnt > 1 {
		for i := 1; i <= m.Pcnt; i++ {
			pname := m.Name + "_" + strconv.Itoa(i)
			if _, ok := s.Processes[pname]; ok {
				s.Processes[pname].Kill()
				delete(s.Processes, pname)
			}
		}
	} else {
		if _, ok := s.Processes[m.Name]; ok {
			s.Processes[m.Name].Kill()
			delete(s.Processes, m.Name)
		}
	}
}

func (s *Supervisor) GetStatus(m Manifest) map[string]string {
	status := map[string]string{}
	if m.Pcnt > 1 {
		for i := 1; i <= m.Pcnt; i++ {
			pname := m.Name + "_" + strconv.Itoa(i)
			if _, ok := s.Processes[pname]; ok {
				status[pname] = s.Processes[pname].Status
			}
		}
	} else {
		if _, ok := s.Processes[m.Name]; ok {
			status[m.Name] = s.Processes[m.Name].Status
		}
	}
	return status
}

func closeDial(cmd *exec.Cmd) {
	if w, ok := cmd.Stdout.(LogWriter); ok {
		w.Close()
	}
	if w, ok := cmd.Stderr.(LogWriter); ok {
		w.Close()
	}
}
