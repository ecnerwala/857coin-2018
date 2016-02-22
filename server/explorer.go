package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/syndtr/goleveldb/leveldb/util"
)

// TODO currently updates every minute, but could update every new block
type explorer struct {
	mu       sync.Mutex
	buf      []byte
	tick     *time.Ticker
	template *template.Template

	Nodes  template.JS
	Edges  template.JS
	Height template.JS

	server *http.Server
}

func NewExplorer(addr string) *explorer {
	e := &explorer{
		tick:     time.NewTicker(1 * time.Minute),
		template: template.Must(template.ParseFiles("templates/explore.html")),
		server: &http.Server{
			Addr:        addr,
			Handler:     LogHandler(http.DefaultServeMux),
			ReadTimeout: 10 * time.Second,
		},
	}
	err := e.update()
	if err != nil {
		log.Println("error updating: ", err)
	}

	http.HandleFunc("/", indexHandler)
	http.HandleFunc("/add", addHandler)
	http.HandleFunc("/next", nextHandler)
	http.HandleFunc("/scores", scoresHandler)
	http.Handle("/block/", http.StripPrefix("/block/", http.HandlerFunc(blockHandler)))

	http.HandleFunc("/explore", e.handler)

	staticHandler := http.FileServer(http.Dir("static"))
	http.Handle("/static/", http.StripPrefix("/static/", staticHandler))

	err = e.server.ListenAndServe()
	if err != nil {
		log.Fatal("ListenAndServe: ", err)
	}

	return e
}

func (e *explorer) handler(w http.ResponseWriter, r *http.Request) {
	select {
	case <-e.tick.C:
		if err := e.update(); err != nil {
			httpError(w, http.StatusInternalServerError, "error updating explorer: %s", err)
			return
		}
		break
	default:
		break
	}

	e.mu.Lock()
	w.Write(e.buf)
	e.mu.Unlock()
}

func (e *explorer) update() error {
	nodes := new(bytes.Buffer)
	edges := new(bytes.Buffer)
	height := bchain.head.BlockHeight

	bchain.Lock()
	iter := bchain.db.NewIterator(util.BytesPrefix([]byte(HeaderBucket)), nil)
	for iter.Next() {
		// Load header
		headerBytes := iter.Value()
		var pheader processedHeader
		if err := json.Unmarshal(headerBytes, &pheader); err != nil {
			bchain.Unlock()
			return err
		}

		parentID := pheader.Header.ParentID

		hash := pheader.Header.Sum()
		label, err := bchain.getBlock(hash)
		if err != nil {
			bchain.Unlock()
			return err
		}
		trunc := 64
		if len(label) < trunc {
			trunc = len(label)
		}
		label = label[:trunc]

		fmt.Fprintf(nodes, "{id:'%x',level:%d,label:'%s'},\n", hash[:], pheader.BlockHeight, label)
		fmt.Fprintf(edges, "{from:'%x',to:'%x'},\n", parentID, hash[:])
	}
	bchain.Unlock()

	e.mu.Lock()
	e.Nodes = template.JS(nodes.String())
	e.Edges = template.JS(edges.String())
	e.Height = template.JS(fmt.Sprintf("%dpx", (height+3)*65))

	buf := new(bytes.Buffer)
	if err := e.template.Execute(buf, e); err != nil {
		e.mu.Unlock()
		return fmt.Errorf("template error: %s", err)
	}
	e.buf = buf.Bytes()
	e.mu.Unlock()

	return nil
}
