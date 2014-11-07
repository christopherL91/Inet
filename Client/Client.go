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
	"encoding/json"
	"github.com/christopherL91/Progp-Inet/Protocol"
	"io"
	"log"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	address = "localhost:3000"
)

type (
	Client struct {
		//channel for commands from user.
		inputCh chan string
		//channel for messages to server.
		writeCh chan *Protocol.Message
		//holds the incoming menu
		menu  *Protocol.MenuData
		mutex *sync.Mutex
	}
)

func init() {
	runtime.GOMAXPROCS(runtime.NumCPU())
}

func newClient() *Client {
	return &Client{
		inputCh: make(chan string, 10),
		writeCh: make(chan *Protocol.Message, 10),
		menu:    new(Protocol.MenuData),
		mutex:   new(sync.Mutex),
	}
}

func main() {
	client := newClient()
	//dial server and get connection. 30s timeout is set.
	conn, err := net.DialTimeout("tcp", address, 30*time.Second)
	if err != nil {
		log.Fatal(err)
	}
	//close connection before exiting program.
	defer conn.Close()
	log.Printf("You are connected to the server at %s", address)
	//read from stdin all the time.
	go func() {
		reader := bufio.NewReader(os.Stdin)
		for {
			line, _ := reader.ReadString('\n')
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			client.inputCh <- line
		}
	}()
	//start listening on incoming messages
	client.start(conn)
}

// Start the basic client services.
func (c *Client) start(conn net.Conn) {
	go c.write(conn)
	// Blocking until server disconnects.
	c.read(conn)
}

// Start listening on messages from server.
func (c *Client) read(conn net.Conn) {
	reader := bufio.NewReader(conn)
	var menu_data bytes.Buffer
	for {
		code, err := reader.Peek(1)
		if err == io.EOF {
			log.Println("Server disconnected")
			return
		} else if err == io.EOF {
			log.Fatalln("Server disconnected")
		}
		log.Printf("Message code:%d", code[0])
		// Check message code
		switch code[0] {
		case Protocol.Balancecode, Protocol.Depositcode, Protocol.Withdrawcode:
			message := new(Protocol.Message)
			err := binary.Read(reader, binary.LittleEndian, message)
			if err != nil {
				log.Println(err)
				return
			}
			log.Printf("Message from server:%v", message)
		case Protocol.Menucode:
			menu_buffer := make([]byte, 10)
			size, _ := reader.Read(menu_buffer)
			menu := new(Protocol.MenuData)
			if menu_buffer[size-1] == 0 {
				// Remove all zeros from the message.
				msg := bytes.TrimRightFunc(menu_buffer[1:size-1], func(x rune) bool {
					return x == 0
				})
				menu_data.Write(msg)
				err := json.Unmarshal(menu_data.Bytes(), menu)
				if err != nil {
					log.Println(err)
					return
				}
				// Reset buffer to default state.
				menu_data.Reset()
				// Make new menu available to system.
				c.addMenu(menu)
				log.Printf("Menu from server:%v", menu)
			} else {
				menu_data.Write(menu_buffer[1:size])
			}
		default:
			log.Println("Unknown message code")
		}
	}
}

func (c *Client) write(conn net.Conn) {
	for {
		switch <-c.inputCh {
		case "send":
			msg := &Protocol.Message{Code: Protocol.Balancecode, Payload: 3}
			log.Printf("About to send this message:%v", msg)
			if err := binary.Write(conn, binary.LittleEndian, msg); err != nil {
				log.Println(err)
				return
			}
		default:
			log.Println("Unknown command")
		}
	}
}

// Adds a new menu to the system.
func (c *Client) addMenu(menu *Protocol.MenuData) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.menu = menu
}

// func (c *Client) loadMenuFromFile() error {
// 	data, err := ioutil.ReadFile("menu.json")
// 	if err != nil {
// 		return err
// 	}
// 	menu := new(Protocol.MenuData)
// 	err = json.Unmarshal(data, menu)
// 	if err != nil {
// 		return err
// 	}
// 	c.addMenu(menu)
// 	return nil
// }
