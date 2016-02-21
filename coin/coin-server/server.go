package main

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/davidlazar/6.857coin/coin"
	db "github.com/syndtr/goleveldb/leveldb"
)

const (
	HeaderPath = "headers"
	BlockPath  = "blocks"
)

type server struct {
	mu     sync.Mutex
	blocks map[coin.Hash]*coin.Block
	head   *coin.Block

	nextMu sync.Mutex
	next   map[coin.Hash]*coin.Block

	scoresMu sync.Mutex
	scores   map[string]int

	spam []*coin.Block
}

var (
	GenesisHeader = Header{
		PrevHash: coin.Hash{},
		MerkleRoot: Hash{0xe3, 0xb0, 0xc4, 0x42, 0x98, 0xfc, 0x1c, 0x14, 0x9a, 0xfb,
			0xf4, 0xc8, 0x99, 0x6f, 0xb9, 0x24, 0x27, 0xae, 0x41, 0xe4, 0x64, 0x9b,
			0x93, 0x4c, 0xa4, 0x95, 0x99, 0x1b, 0x78, 0x52, 0xb8, 0x55},
		Difficulty: 2,
		Timestamp:  time.Now(),
		Nonces:     [coin.NumCollisions]uint32{1, 1, 1},
		Version:    0x00,
	}
)

type (
	server struct {
		sync.Mutex

		head         coin.Header
		scores       map[string]int
		heightToHash map[int]coin.Hash

		headerDB *db.Db
		blockDB  *db.Db
	}

	processedHeader struct {
		header         coin.Header `json:"header"`
		blockHeight    uint32      `json:"blockheight"`
		isMainChain    bool        `json:"ismainchain"`
		totalDiffuclty uint64      `json:"totaldiff"`
	}
)

func NewServer() (*Server, error) {
	s := Server{}

	if err := s.loadScores(); err != nil {
		return nil, err
	}

	if err := s.loadHeightToHash(); err != nil {
		return nil, err
	}

	// Check for genesis block
	if _, ok := s.heightToHash[0]; !ok {
		// Attempt to write genesis block
		if err := s.addBlock(GenesisHeader, nil); err != nil {
			return nil, err
		}
	}

	return s, nil
}

func (s *server) initDB() {
	scoreDB, err := db.OpenFile(ScorePath, nil)
	if err != nil {
		log.Println(err)
		panic("Unable to open score database")
	}
	s.scoreDB = scoreDB

	headerDB, err := db.OpenFile(HeaderPath, nil)
	if err != nil {
		log.Println(err)
		panic("Unable to open header database")
	}
	s.headerDB = headerDB

	blockDB, err := db.OpenFile(BlockPath, nil)
	if err != nil {
		log.Println(err)
		panic("Unable to open block database")
	}
	s.blockDB = blockDB
}

func (s *server) loadScores() error {
	s.Lock()
	defer s.Unlock()

	s.scores = make(map[string]int)

	// Iterate over all headers, add to score if version 0
	iter := s.headerDB.NewIterator(nil, nil)
	for iter.Next() {
		// Load header
		headerBytes := iter.Value()
		var pheader processedHeader
		if err := json.Unmarshal(headerBytes, pheader); err != nil {
			return err
		}

		// Add to score map if version 0
		if pheader.Version == 0 {
			// Load block data and convert to string
			blockBytes, err := getBlock(pheader.Header.Sum())
			if err != nil {
				return err
			}
			teamname := string(blockBytes)

			// Increment team's score
			if total, ok := s.scores[teamname]; ok {
				s.scores[teamname] = total + 1
			} else {
				s.scores[teamname] = 1
			}
		}
	}

	return nil
}

func (s *Server) loadHeightToHash() error {
	s.Lock()
	defer s.Unlock()

	s.heightToHash = make(map[int]coin.Hash)

	iter := s.headerDB.NewIterator(nil, nil)
	for iter.Next() {
		// Unmarshal processedHeader
		b := iter.Value()
		var pheader processedHeader
		err := json.Unmarshal(b, &pheader)
		if err != nil {
			return err
		}

		// Add to heightToHash map if header is in main chain
		if pheader.isMainChain {
			s.heightToHash[pheader.blockHeight] = pheader.Header.Sum()
		}
	}

	return nil
}

func (s *server) loadBlocks(dir string) error {
	paths, err := filepath.Glob(filepath.Join(dir, "*.block"))
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return fmt.Errorf("no blocks found")
	}

	maxlen := uint32(0)
	for _, path := range paths {
		j, err := ioutil.ReadFile(path)
		if err != nil {
			return err
		}
		b := new(coin.Block)
		err = json.Unmarshal(j, b)
		if err != nil {
			return fmt.Errorf("json error (%s): %s", path, err)
		}
		sum := b.Sum()
		s.blocks[sum] = b
		if b.Length >= maxlen {
			s.head = b
			maxlen = b.Length
		}
	}
	s.updateNext()
	s.updateScores()
	s.updateSpam()

	return nil
}

func (s *server) updateNext() {
	s.nextMu.Lock()
	s.next = make(map[coin.Hash]*coin.Block)
	for _, block := range s.blocks {
		s.next[block.PrevHash] = block
	}
	s.nextMu.Unlock()
}

func (s *server) updateScores() {
	s.scoresMu.Lock()
	s.scores = make(map[string]int)
	head := s.head
	for head != nil {
		s.scores[head.Contents] += 1
		prev := head.PrevHash
		head = srv.blocks[prev]
	}
	s.scoresMu.Unlock()
}

func (s *server) updateSpam() {
	s.spam = make([]*coin.Block, 0, 1024)
	for _, block := range s.blocks {
		if block.Length == 1 {
			if len(block.Contents) < 32 {
				continue
			}
			sum := block.Sum()
			if next, ok := s.next[sum]; !ok || next == nil {
				s.spam = append(s.spam, block)
			}
		}
	}
}

func (s *server) addBlock(h coin.Header, b coin.Block) error {
	s.Lock()
	defer s.Unlock()

	// Only process valid blocks
	if err := h.Valid(b); err != nil {
		return err
	}

	// Build processedHeader
	var ph processedHeader
	if len(s.heightToHash) == 0 {
		// Process genesis header
		ph := processedHeader{
			header:      h,
			blockheight: h.Difficulty,
			isMainChain: true,
		}
	} else {
		// Check that block extends existing header
		prevHeader, err := s.getHeader(h.ParentID)
		if err != nil {
			return err
		}

		ph := processedHeader{
			header:         h,
			blockheight:    prevHeader.blockheight + 1,
			totalDiffuclty: prevHeader.totalDiffuclty + h.Difficulty,
		}
	}

	if ph.totalDiffuclty > s.head.totalDiffuclty {
		if err := s.swapMainFork(ph); err != nil {
			return err
		}
	}

	if err := s.putHeader(ph); err != nil {
		return err
	}

	// Save block under H(header)
	headerID := h.Sum()

	return s.putBlock(headerID, b)
}

func (s *server) putHeader(h processedHeader) error {
	headerJson, err := json.Marshal(h)
	if err != nil {
		return err
	}

	id := h.Header.Sum()

	return s.headerDB.Put(id[:], headerJson)
}

func (s *server) getHeader(h coin.Hash) (*processedHeader, error) {
	headerBytes, err := s.headerDB.Get(h[:], nil)
	if err != nil {
		return nil, err
	}

	var pheader *processedHeader
	err = json.Unmarshal(headerBytes, &pheader)

	return pheader, err
}

func (s *server) putBlock(id coin.Hash, b coin.Block) error {
	return s.blockDB.Put(id[:], []byte(b))
}

func (s *server) getBlock(h coin.Hash) (coin.Block, error) {
	blockBytes, err := s.blockDB.Get(h[:], nil)
	if err != nil {
		return nil, err
	}

	return coin.Block(blockBytes), nil
}

func (s *server) swapMainFork(ph processedHeader) error {

}

func (s *server) addBlock(b *coin.Block) error {
	srv.mu.Lock()
	prev, ok := s.blocks[b.PrevHash]
	srv.mu.Unlock()
	if !ok {
		return fmt.Errorf("previous block not found: %x", b.PrevHash[:])
	}

	if b.Length != prev.Length+1 {
		return fmt.Errorf("invalid length: got %d, expecting %d", b.Length, prev.Length+1)
	}

	if !validContents(b.Contents) {
		return fmt.Errorf("invalid block contents: %s", b.Contents)
	}

	h, ok := b.Verify()
	if !ok {
		return fmt.Errorf("insufficient leading zero bits: %x", h[:])
	}

	b.Timestamp = time.Now().UTC()

	j, err := json.Marshal(b)
	if err != nil {
		return fmt.Errorf("json encoding error: %s", err)
	}
	err = ioutil.WriteFile(filepath.Join(*dataDir, h.String()+".block"), j, 0640)
	if err != nil {
		return fmt.Errorf("error writing block file: %s", err)
	}

	s.mu.Lock()
	s.blocks[h] = b
	if b.Length > srv.head.Length {
		s.head = b
		s.updateScores()
	}
	s.updateNext()
	s.mu.Unlock()
	return nil
}

func validContents(s string) bool {
	if len(s) > 64 {
		return false
	}
	if _, err := strconv.Atoi(s); err == nil {
		return true
	}
	if x, err := hex.DecodeString(s); err == nil && len(x) == 32 {
		return true
	}
	return false
}

func addHandler(w http.ResponseWriter, r *http.Request) {
	block := new(coin.Block)
	if err := json.NewDecoder(r.Body).Decode(block); err != nil {
		httpError(w, http.StatusBadRequest, "error parsing block json: %s", err)
		return
	}
	if err := srv.addBlock(block); err != nil {
		httpError(w, http.StatusBadRequest, "failed to add block: %s", err)
		return
	}
	w.Write([]byte("Ok"))
}

func headHandler(w http.ResponseWriter, r *http.Request) {
	srv.mu.Lock()
	head := srv.head
	srv.mu.Unlock()

	j, err := json.MarshalIndent(head, "", "  ")
	if err != nil {
		httpError(w, http.StatusInternalServerError, "json encoding error: %s", err)
		return
	}
	w.Write(j)
}

func blockHandler(w http.ResponseWriter, r *http.Request) {
	h, err := coin.NewHash(r.URL.Path)
	if err != nil {
		httpError(w, http.StatusBadRequest, "error reading hash: %s", err)
		return
	}

	srv.mu.Lock()
	b, ok := srv.blocks[h]
	srv.mu.Unlock()
	if !ok {
		httpError(w, http.StatusNotFound, "block not found: %x", h[:])
		return
	}

	j, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		httpError(w, http.StatusInternalServerError, "json encoding error: %s", err)
		return
	}
	w.Write(j)
}

func nextHandler(w http.ResponseWriter, r *http.Request) {
	h, err := coin.NewHash(r.URL.Path)
	if err != nil {
		httpError(w, http.StatusBadRequest, "error reading hash: %s", err)
		return
	}

	srv.nextMu.Lock()
	b, ok := srv.next[h]
	srv.nextMu.Unlock()
	if !ok {
		// TODO we dont need to log this
		httpError(w, http.StatusNotFound, "no next block for block: %x", h[:])
		return
	}

	j, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		httpError(w, http.StatusInternalServerError, "json encoding error: %s", err)
		return
	}
	w.Write(j)
}

func scoresHandler(w http.ResponseWriter, r *http.Request) {
	srv.scoresMu.Lock()
	j, err := json.MarshalIndent(srv.scores, "", "  ")
	srv.scoresMu.Unlock()
	if err != nil {
		httpError(w, http.StatusInternalServerError, "json encoding error: %s", err)
		return
	}
	w.Write(j)
}

func spamHandler(w http.ResponseWriter, r *http.Request) {
	for _, b := range srv.spam {
		sum := b.Sum()
		fmt.Fprintf(w, "%x %s\n", sum[:], b.Contents)
	}
}

// TODO currently updates every minute, but could update every new block
type explorer struct {
	mu       sync.Mutex
	buf      []byte
	tick     *time.Ticker
	template *template.Template

	Nodes  template.JS
	Edges  template.JS
	Height template.JS
}

func newExplorer() *explorer {
	e := &explorer{
		tick:     time.NewTicker(1 * time.Minute),
		template: template.Must(template.ParseFiles("templates/explore.html")),
	}
	e.update()
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
	height := srv.head.Length

	srv.mu.Lock()
	for hash, block := range srv.blocks {
		var label string
		switch {
		case block.Contents == "Genesis":
			label = "Genesis"
		case len(block.Contents) < 5:
			label = block.Contents
		default:
			label = block.Contents[0:5]
		}
		fmt.Fprintf(nodes, "{id:'%x',level:%d,label:'%s'},\n", hash[:], height-block.Length, label)
		fmt.Fprintf(edges, "{from:'%x',to:'%x'},\n", block.PrevHash[:], hash[:])
	}
	srv.mu.Unlock()

	e.mu.Lock()
	e.Nodes = template.JS(nodes.String())
	e.Edges = template.JS(edges.String())
	e.Height = template.JS(fmt.Sprintf("%dpx", (height+3)*65))

	buf := new(bytes.Buffer)
	if err := e.template.Execute(buf, e); err != nil {
		return fmt.Errorf("template error: %s", err)
	}
	e.buf = buf.Bytes()
	e.mu.Unlock()

	return nil
}

func indexHandler(w http.ResponseWriter, r *http.Request) {
	data, err := ioutil.ReadFile("templates/index.html")
	if err != nil {
		httpError(w, http.StatusInternalServerError, "error reading index: %s", err)
	}
	w.Write(data)
}

var dataDir = flag.String("data", "blocks", "data directory")
var addr = flag.String("addr", ":8080", "http service address")

var srv *server

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())
	flag.Parse()

	srv = &server{
		blocks: make(map[coin.Hash]*coin.Block),
	}
	if err := srv.loadBlocks(*dataDir); err != nil {
		log.Fatalf("error loading blocks: %s", err)
	}

	httpServer := &http.Server{
		Addr:    *addr,
		Handler: LogHandler(http.DefaultServeMux),
		//ErrorLog:  errorLogger,
	}

	http.HandleFunc("/", indexHandler)
	http.HandleFunc("/add", addHandler)
	http.HandleFunc("/head", headHandler)
	http.HandleFunc("/scores", scoresHandler)
	http.HandleFunc("/spam", spamHandler)
	http.Handle("/next/", http.StripPrefix("/next/", http.HandlerFunc(nextHandler)))
	http.Handle("/block/", http.StripPrefix("/block/", http.HandlerFunc(blockHandler)))

	explorer := newExplorer()
	http.HandleFunc("/explore", explorer.handler)

	staticHandler := http.FileServer(http.Dir("static"))
	http.Handle("/static/", http.StripPrefix("/static/", staticHandler))

	err := httpServer.ListenAndServe()
	if err != nil {
		log.Fatal("ListenAndServe: ", err)
	}
}

func httpError(w http.ResponseWriter, status int, format string, v ...interface{}) {
	s := fmt.Sprintf(http.StatusText(status)+": "+format, v...)
	log.Print(s)
	http.Error(w, s, status)
}

var accessLogger *log.Logger
var accessLogBuffer *bufio.Writer

func LogHandler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accessLogger.Printf("%s %s %s %s %q %q", StripPort(r.RemoteAddr), r.Method, r.URL, r.Proto, r.Referer(), r.UserAgent())
		h.ServeHTTP(w, r)
	})
}

func init() {
	logPath := "logs/" + time.Now().Format("2006-01-02_15:04:05")
	accessFile, err := os.Create(logPath)
	if err != nil {
		log.Fatalf("%v", err)
	}
	accessLogBuffer = bufio.NewWriter(accessFile)
	accessLogger = log.New(accessLogBuffer, "", log.LstdFlags)
}

func StripPort(s string) string {
	if i := strings.LastIndex(s, ":"); i != -1 {
		return s[:i]
	}
	return s
}
