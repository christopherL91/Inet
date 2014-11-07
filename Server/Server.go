// The MIT License (MIT)

// Copyright (c) 2014 Christopher Lillthors

// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:

// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.

// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"flag"
	"github.com/christopherL91/Progp-Inet/Protocol"
	"io"
	"io/ioutil"
	"log"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
)

type (
	Server struct {
		//a simple mutex for the maps.
		mutex *sync.Mutex
		//only a list of connections, the key is nothing.
		connections map[net.Conn]struct{}
		//connection => cardnumber
		clients map[net.Conn]int64
		//channel for sending messages through
		messageCh chan *Protocol.Message
		//channel for sending menus through
		menuCh chan *Protocol.Menu
		//channel for commands from user (stdin)
		inputCh chan string
	}
)

const (
	//address to server
	address = "localhost:3000"
)

var (
	//debug flag.
	debug bool
)

func init() {
	flag.BoolVar(&debug, "debug", false, "debug information")
	flag.Parse()
	runtime.GOMAXPROCS(runtime.NumCPU())
}

func newServer() *Server {
	return &Server{
		mutex:       new(sync.Mutex),
		connections: make(map[net.Conn]struct{}),
		clients:     make(map[net.Conn]int64),
		menuCh:      make(chan *Protocol.Menu, 10),
		messageCh:   make(chan *Protocol.Message, 10),
		inputCh:     make(chan string, 10),
	}
}

//This function distributes all the messages/menus to the clients
func (s *Server) distributor() {
	for {
		select {
		case message := <-s.messageCh:
			if debug {
				log.Println(message)
			}
			s.mutex.Lock()
			for conn, _ := range s.connections {
				if err := binary.Write(conn, binary.LittleEndian, message); err != nil {
					log.Println(err)
					return
				}
			}
			s.mutex.Unlock()
		case menu := <-s.menuCh: //distribute the 10 bytes to all the connections.
			if debug {
				log.Println(menu)
			}
			s.mutex.Lock()
			for conn, _ := range s.connections {
				if err := binary.Write(conn, binary.LittleEndian, menu); err != nil {
					log.Println(err)
					return
				}
			}
			s.mutex.Unlock()
		}
	}
}

//remove disconnecting user.
func (s *Server) removeConnection(conn net.Conn) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	delete(s.connections, conn)
	if debug {
		log.Println("Number of clients", len(s.connections))
	}
}

//add new connection to list of connections.
func (s *Server) addConnection(conn net.Conn) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.connections[conn] = struct{}{}
	if debug {
		log.Println("Number of clients", len(s.connections))
	}
}

//read input from command line
func (s *Server) readInput() {
	reader := bufio.NewReader(os.Stdin)
	for {
		line, _ := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		s.inputCh <- line
	}
}

//starts the actual server services.
func (s *Server) start() {
	go s.distributor()
	go s.readInput()
}

func main() {
	server := newServer()
	go server.start()
	//start listening on address.
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
		//add a connection to the map of connections.
		server.addConnection(conn)
		go server.clientHandler(conn)
	}
}

//take care of the client.
func (s *Server) clientHandler(conn net.Conn) {
	defer s.removeConnection(conn)
	defer conn.Close()
	go s.read(conn)
	s.write(conn)
}

func (s *Server) read(conn net.Conn) {
	reader := bufio.NewReader(conn)
	for {
		code, err := reader.Peek(1)
		if err != nil {
			log.Println(err)
			return
		}
		if debug {
			log.Println("Code:", code[0])
		}
		//check message code
		switch code[0] {
		case Protocol.Balancecode, Protocol.Depositcode, Protocol.Withdrawcode:
			message := new(Protocol.Message)
			err := binary.Read(reader, binary.LittleEndian, message)
			if err != nil {
				log.Println(err)
				return
			}
			if debug {
				log.Println(message)
			}
		case Protocol.RequestMenucode:
			//TODO
		default:
			log.Println("Something else")
		}
	}
}

func (s *Server) write(conn net.Conn) {
	for {
		switch <-s.inputCh {
		case "message":
			log.Println("Sending message to clients")
			message := &Protocol.Message{Code: Protocol.Balancecode, Payload: 2}
			s.messageCh <- message
		case "menu":
			log.Println("Sending menu to clients")
			file, err := ioutil.ReadFile("menu.json")
			if err != nil {
				panic(err)
			}
			//create new buffer with the file as content.
			json_data := bytes.NewBuffer(file)
			if debug {
				log.Println(json_data.String())
			}
			//add zero to end of buffer, for client to know when to stop reading.
			json_data.WriteByte(0)
			for {
				buffer := make([]byte, 9)
				//fill buffer with 9 bytes at a time.
				_, err := json_data.Read(buffer)
				if err == io.EOF {
					break
				}
				//create a fixed slice for the message.
				var fixed_slice [9]byte
				//copy content to fixed_slice
				copy(fixed_slice[:], buffer)
				//send menu to distributor
				s.menuCh <- &Protocol.Menu{Code: Protocol.Menucode, Payload: fixed_slice}
			}
		default:
			log.Println("Unknown command")
		}
	}
}
