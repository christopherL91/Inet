package main

import (
	"bufio"
	"encoding/binary"
	"github.com/christopherL91/Protocol"
	"log"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
)

type (
	st struct{}

	Server struct {
		mutex       *sync.Mutex
		connections map[net.Conn]st
		clients     map[net.Conn]int64
		emitCh      chan *Protocol.Message
		messageCh   chan *Protocol.Message
		menuCh      chan []byte
		writeCh     chan string
	}

	Menu struct {
		Payload []byte
	}
)

const (
	address = "localhost:3000"
)

func init() {
	runtime.GOMAXPROCS(runtime.NumCPU())
}

func newServer() *Server {
	return &Server{
		mutex:       new(sync.Mutex),
		connections: make(map[net.Conn]st),
		clients:     make(map[net.Conn]int64),
		emitCh:      make(chan *Protocol.Message, 10),
		menuCh:      make(chan []byte, 10),
		messageCh:   make(chan *Protocol.Message, 10),
		writeCh:     make(chan string, 10),
	}
}

// func (s *Server) distributer() {
// 	for {
// 		select {
// 		case message := <-s.emitCh:
// 			s.mutex.Lock()
// 			for conn, _ := range s.connections {

// 			}
// 			s.mutex.Unlock()
// 		}
// 	}
// }

func (s *Server) removeConnection(conn net.Conn) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	delete(s.connections, conn)
}

func (s *Server) addConnection(conn net.Conn) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.connections[conn] = st{}
}

func (s *Server) readInput() {
	reader := bufio.NewReader(os.Stdin)
	for {
		line, _ := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		s.writeCh <- line
	}
}

func (s *Server) start() {
	// go s.distributer()
	go s.readInput()
}

func main() {
	server := newServer()
	go server.start()
	l, err := net.Listen("tcp", address)
	if err != nil {
		log.Fatal(err)
	}
	defer l.Close()
	for {
		conn, err := l.Accept()
		if err != nil {
			log.Println(err)
			continue
		}
		server.addConnection(conn)
		go server.clientHandler(conn)
	}
}

func (s *Server) read(conn net.Conn) {
	reader := bufio.NewReader(conn)
	for {
		code, err := reader.Peek(1)
		if err != nil {
			log.Println(err)
			return
		}
		//check message code
		switch code[0] {
		case 1, 2, 3:
			message := new(Protocol.Message)
			err := binary.Read(reader, binary.LittleEndian, message)
			if err != nil {
				log.Println(err)
				return
			}
			log.Println(message)
		default:
			log.Println("Something else")
		}
	}
}

func (s *Server) write(conn net.Conn) {
	for {
		if <-s.writeCh == "test" {
			log.Println("Sending data to client...")
			message := &Protocol.Message{Code: 1, Payload: 1}
			err := binary.Write(conn, binary.LittleEndian, message)
			if err != nil {
				log.Println(err)
				return
			}
		}
	}
}

func (s *Server) clientHandler(conn net.Conn) {
	defer s.removeConnection(conn)
	defer conn.Close()
	go s.read(conn)
	s.write(conn)
}
