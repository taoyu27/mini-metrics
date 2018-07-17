package metrics

import (
	"fmt"
	"net/http"
	
	"reflect"
	"time"
	"sync"
)

type Metrics struct {
	name 		string
	pipe		chan string
	data		string
	quit		chan struct{}
	isQuit		bool
	interval	time.Duration
	
	mu			sync.Mutex
}
	
func (m *Metrics)ServeHTTP(w http.ResponseWriter, r *http.Request){
	switch r.URL.Path {
		case "/metrics":
			fmt.Fprintf(w, "[%s] Hello you %s", m.name, m.data)
		case "/interval":
			newInterval := r.URL.Query().Get("value")
			if newInterval == "" {
				w.WriteHeader(http.StatusNotFound)
				fmt.Fprintf(w, "Please input interval value,\n")
			} else {
				fmt.Println(reflect.TypeOf(newInterval))
				du, err := time.ParseDuration(newInterval)
				if err != nil {
					w.WriteHeader(http.StatusBadRequest)
					fmt.Fprintf(w, "Please input interval value as time format,\n")
				} else {
					fmt.Fprintf(w, "[%s] Change interval to %v\n", m.name, du)
					m.interval = du
				}
			}
		case "/start":
			m.mu.Lock()
			if !m.isQuit {
				w.WriteHeader(http.StatusForbidden)
				fmt.Fprintf(w, "[%s] Collectors already running.\n", m.name)
			} else {
				m.quit = make(chan struct{})
				go m.collect()
				go m.dataman()
				fmt.Fprintf(w, "[%s] Collectors is running again.\n", m.name)
			}
			m.mu.Unlock()
		case "/stop":
			if !m.isQuit { 
				m.mu.Lock()
				close(m.quit)
				m.isQuit = true
				m.mu.Unlock()
			}
	}
}

func (m *Metrics)collect(){
	ticker := time.NewTimer(m.interval)
	for {
		select {
			case <-ticker.C:
				fmt.Printf("receive tick\n")
				m.pipe<-time.Now().String()	
				ticker = time.NewTimer(m.interval)
			case <-m.quit:
				fmt.Printf("Byebye collector man.\n")
				return
		}
	}
}

func (m *Metrics)dataman(){
	for {
		select {
			case m.data = <-m.pipe:
				fmt.Printf("receive data %s\n", m.data)
			case <-m.quit:
				fmt.Printf("Byebye data man.\n")
				return
		}
	}
}

func NewMetrics(s string, t time.Duration)*Metrics{
	var m = &Metrics{
		name: s,
		pipe: make(chan string),
		data: "init data",
		quit: make(chan struct{}),
		isQuit: false,
		mu:	sync.Mutex{},
		interval: t,
	}
	go m.collect()
	go m.dataman()
	return m
}

