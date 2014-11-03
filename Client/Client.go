package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"github.com/christopherL91/Protocol"
	"log"
	"net"
	"os"
	"runtime"
	"strings"
	"time"
)

const (
	address = "localhost:3000"
)

type (
	Client struct {
		inputCh chan string
		writeCh chan *Protocol.Message
	}
)

func init() {
	runtime.GOMAXPROCS(runtime.NumCPU())
}

func newClient() *Client {
	return &Client{
		inputCh: make(chan string, 10),
		writeCh: make(chan *Protocol.Message, 10),
	}
}

func main() {
	client := newClient()
	conn, err := net.DialTimeout("tcp", address, 30*time.Second)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	//start listening on incoming messages
	go client.start(conn)

	reader := bufio.NewReader(os.Stdin)
	for {
		line, _ := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		client.inputCh <- line
	}
}

func (c *Client) start(conn net.Conn) {
	go c.read(conn)
	go c.write(conn)

	for {
		if <-c.inputCh == "send" {
			log.Println("Sending data...")
			message := &Protocol.Message{Code: 1, Payload: 2}
			c.writeCh <- message
		}
	}
}

func (c *Client) read(conn net.Conn) {
	reader := bufio.NewReader(conn)
	var menu_data bytes.Buffer
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
		case 4:
			menu_buffer := make([]byte, 10)
			size, _ := reader.Read(menu_buffer)
			menu_data.Write(menu_buffer)
			if menu_buffer[size-1] == 0 {
				menu := make(map[string]interface{})
				menu_data.Truncate(menu_data.Len() - 1)
				err := json.Unmarshal(menu_data.Bytes(), menu)
				if err != nil {
					log.Println(err)
					return
				}
			}
		default:
			log.Println("Something else")
		}
	}
}

func (c *Client) write(conn net.Conn) {
	for {
		message := <-c.writeCh
		err := binary.Write(conn, binary.LittleEndian, message)
		if err != nil {
			log.Println(err)
			return
		}
	}
}
