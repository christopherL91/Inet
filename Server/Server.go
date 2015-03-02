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
	"database/sql"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/christopherL91/Progp-Inet/Protocol"
	_ "github.com/lib/pq"
)

type (
	Server struct {
		// A simple mutex for the maps.
		mutex *sync.Mutex
		// Only a list of connections, the key is nothing.
		connections map[net.Conn]struct{}
		// Channel for commands from user (stdin)
		inputCh chan string
	}
)

var (
	base    string
	port    string
	timeout = 15 * time.Minute
	db      = new(sql.DB)
)

func init() {
	var err error
	flag.StringVar(&base, "address", "localhost", "The base address to start the server on")
	flag.StringVar(&port, "port", "3000", "The port to start the server on")
	flag.Parse()
	db, err = sql.Open("postgres", "user=christopher dbname=inet sslmode=disable")
	if err != nil {
		panic(err)
	}
	runtime.GOMAXPROCS(runtime.NumCPU())
}

func newServer() *Server {
	return &Server{
		mutex:       &sync.Mutex{},
		connections: make(map[net.Conn]struct{}),
		inputCh:     make(chan string, 100),
	}
}

func main() {
	c := make(chan os.Signal)      //A channel to listen on keyboard events.
	signal.Notify(c, os.Interrupt) //If user pressed CTRL - C.
	server := newServer()
	go server.cleanUp(c)
	go server.readInput() // Listen on keyboard events.
	// Base:Port
	address := net.JoinHostPort(base, port)
	// Start listening on address.
	l, err := net.Listen("tcp", address)
	if err != nil {
		log.Fatal(err)
	}
	defer l.Close()
	log.Println("Server started on", address)
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

// Take care of the client.
func (s *Server) clientHandler(conn net.Conn) {
	writeCh := make(chan *Protocol.Message, 10)
	menuCh := make(chan *Protocol.Menu)
	defer func() {
		s.removeConnection(conn)
		conn.Close()
		log.Printf("Client with IP %s disconnected", conn.RemoteAddr().String())
	}()
	log.Printf("Client with IP %s connected", conn.RemoteAddr().String())
	go s.write(conn, writeCh, menuCh)
	s.read(conn, writeCh, menuCh)
}

// Remove disconnecting user.
func (s *Server) removeConnection(conn net.Conn) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	delete(s.connections, conn)
	log.Printf("Number of connections:%d", len(s.connections))
}

// Add new connection to list of connections.
func (s *Server) addConnection(conn net.Conn) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.connections[conn] = struct{}{}
	log.Printf("Number of connections:%d", len(s.connections))
}

// Read input from command line
func (s *Server) readInput() {
	go s.inputCommand()
	reader := bufio.NewReader(os.Stdin)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			log.Println(err)
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		s.inputCh <- line
	}
}

func (s *Server) read(conn net.Conn, writeCh chan<- *Protocol.Message, menuCh chan<- *Protocol.Menu) {
	conn.SetReadDeadline(time.Now().Add(timeout))
	reader := bufio.NewReader(conn)
	loggedin := false
	var cardnumber string
	for {
		code, err := reader.Peek(1)
		if err != nil {
			switch err := err.(type) {
			case net.Error:
				if err.Timeout() {
					fmt.Printf("Client with ip %s disconnected due to timeout\n", conn.RemoteAddr().String())
					return
				}
			default:
				return // Client disconnected
			}
		}
		// log.Printf("Message code:%d", code[0])
		// Extend read deadline
		conn.SetReadDeadline(time.Now().Add(timeout))
		// Check message code
		switch code[0] {

		case Protocol.Balancecode:
			// fmt.Println("client sent balance code")
			message := new(Protocol.Message)
			if err := binary.Read(reader, binary.LittleEndian, message); err != nil {
				log.Println(err)
				return
			}
			if loggedin {
				var balance uint32
				err := db.QueryRow(
					`select sum(transactions.money)
					from transactions
					inner join users on (transactions.user_id = users.id)
					where users.cardnumber = $1`, cardnumber).Scan(&balance)
				if err != nil {
					log.Println(err)
					return
				}
				writeCh <- &Protocol.Message{Code: Protocol.BalanceResponseCode, Payload: balance}
			} else {
				writeCh <- &Protocol.Message{Code: Protocol.BalanceResponseErrorCode}
				continue
			}

		case Protocol.Withdrawcode:
			// fmt.Println("Got withdraw code")
			message := new(Protocol.Message)
			if err := binary.Read(reader, binary.LittleEndian, message); err != nil {
				log.Println(err)
				return
			}
			if loggedin {
				// Check if scratch pin is correct
				if message.Number%2 != 0 {
					writeCh <- &Protocol.Message{Code: Protocol.WithdrawResponseErrorCode}
					continue
				}

				var balance uint32
				err := db.QueryRow(
					`select sum(transactions.money)
					from transactions
					inner join users on (transactions.user_id = users.id)
					where users.cardnumber = $1`, cardnumber).Scan(&balance)
				if err != nil {
					log.Println(err)
					return
				}
				withdraw := -int64(message.Payload)
				current := int64(balance) + withdraw
				if current >= 0 {
					_, err := db.Exec(
						`insert into transactions(transaction_id,money,user_id)
						select uuid_generate_v4(),$2, id from users where cardnumber = $1`, cardnumber, withdraw)
					if err != nil {
						log.Println(err)
						return
					}
					writeCh <- &Protocol.Message{Code: Protocol.WithdrawResponseCode, Payload: uint32(current)}
				} else {
					// Not enough balance
					writeCh <- &Protocol.Message{Code: Protocol.WithdrawResponseErrorCode}
					continue
				}
			} else {
				writeCh <- &Protocol.Message{Code: Protocol.WithdrawResponseErrorCode}
				continue
			}

		case Protocol.Depositcode:
			// fmt.Println("Got deposit code")
			message := new(Protocol.Message)
			if err := binary.Read(reader, binary.LittleEndian, message); err != nil {
				log.Println(err)
				return
			}
			if loggedin {
				var balance uint32
				err := db.QueryRow(
					`select sum(transactions.money)
					from transactions
					inner join users on (transactions.user_id = users.id)
					where users.cardnumber = $1`, cardnumber).Scan(&balance)
				if err != nil {
					log.Println(err)
					return
				}
				deposit := int64(message.Payload)
				current := int64(balance) + deposit
				_, err = db.Exec(
					`insert into transactions(transaction_id,money,user_id)
						select uuid_generate_v4(),$2, id from users where cardnumber = $1`, cardnumber, deposit)
				if err != nil {
					log.Println(err)
					return
				}
				writeCh <- &Protocol.Message{Code: Protocol.DepositResponseCode, Payload: uint32(current)}
			} else {
				writeCh <- &Protocol.Message{Code: Protocol.DepositResponseErrorCode}
				continue
			}

		case Protocol.RequestMenucode:
			// fmt.Println("client requested menu")
			message := new(Protocol.Message)
			if err := binary.Read(reader, binary.LittleEndian, message); err != nil {
				log.Println(err)
				return
			}
			// Read whole file.
			file, err := ioutil.ReadFile("menu.json")
			if err != nil {
				panic(err)
			}
			// Create new buffer with the file as content.
			json_data := bytes.NewBuffer(file)
			// Add zero to end of buffer, for client to know when to stop reading.
			json_data.WriteByte(0)
			for {
				buffer := make([]byte, 9)
				// Fill buffer with 9 bytes at a time.
				_, err := json_data.Read(buffer)
				if err == io.EOF {
					break
				} else {
					// Create a fixed slice for the message.
					var fixed_slice [9]byte
					// Copy content to fixed_slice
					copy(fixed_slice[:], buffer)
					menuCh <- &Protocol.Menu{Code: Protocol.Menucode, Payload: fixed_slice}
				}
			}

		case Protocol.Logoutcode:
			loggedin = false
			message := new(Protocol.Message)
			if err := binary.Read(reader, binary.LittleEndian, message); err != nil {
				log.Println(err)
				return
			}

		case Protocol.LoginCode:
			message := new(Protocol.Message)
			if err := binary.Read(reader, binary.LittleEndian, message); err != nil {
				log.Println(err)
				return
			}

			if !loggedin {
				// Check in db
				var password string
				card := strconv.Itoa(int(message.Number))
				err := db.QueryRow(`select pass from users where cardnumber = $1`, card).Scan(&password)
				if err != nil {
					log.Println(err)
					return
				}
				pass := strconv.Itoa(int(message.Payload))
				if pass == password {
					cardnumber = card
					writeCh <- &Protocol.Message{Code: Protocol.LoginResponseOK}
					loggedin = true
				} else {
					writeCh <- &Protocol.Message{Code: Protocol.LoginResponseError}
					continue
				}
			} else {
				// Already logged in
				writeCh <- &Protocol.Message{Code: Protocol.LoginResponseError}
				continue
			}

		default:
			log.Println("Something else")
			return // Client sent a strange message! Disconnect client.
		}
	}
}

func (s *Server) write(conn net.Conn, writeCh <-chan *Protocol.Message, menuCh <-chan *Protocol.Menu) {
	for {
		select {
		case message := <-writeCh:
			// fmt.Println("sending message to client", message)
			if err := binary.Write(conn, binary.LittleEndian, message); err != nil {
				log.Println(err)
				return
			}
		case menu_slice := <-menuCh:
			// fmt.Println("sending menu chunk to client ", menu_slice)
			if err := binary.Write(conn, binary.LittleEndian, menu_slice); err != nil {
				log.Println(err)
				return
			}
		}
	}
}

func (s *Server) inputCommand() {
	new_menu_notification := &Protocol.Message{Code: Protocol.NewMenucode}
	for {
		switch <-s.inputCh {
		case "menu":
			// fmt.Println("New Menu")
			s.mutex.Lock()
			for conn := range s.connections {
				if err := binary.Write(conn, binary.LittleEndian, new_menu_notification); err != nil {
					log.Println(err)
					return
				}
			}
			s.mutex.Unlock()
		default:
			log.Println("Unknown command")
		}
	}
}

func (s *Server) cleanUp(c chan os.Signal) {
	<-c
	fmt.Println("\nClosing every client connection...")
	defer db.Close()
	s.mutex.Lock()
	defer s.mutex.Unlock()
	for conn := range s.connections {
		if conn == nil {
			continue
		}
		err := conn.Close()
		if err != nil {
			continue
		}
	}
	fmt.Println("Server is now closing...")
	os.Exit(1)
}
