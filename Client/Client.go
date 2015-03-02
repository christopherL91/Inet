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
	"errors"
	"flag"
	"fmt"
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
)

type (
	Client struct {
		// Channel for commands from user.
		inputCh chan string
		// Channel for messages to server.
		writeCh chan *Protocol.Message
		// Channel for distributing balance.
		balanceCh chan *Protocol.Message
		// Channel for distributing deposit response.
		depositCh chan *Protocol.Message
		// Channel for distributing withdraw response.
		withdrawCh chan *Protocol.Message
		// Channel for login messages
		loginCh chan *Protocol.Message
		// Holds the incoming menu
		menu *Protocol.MenuData
		// Simple mutex for menu
		mutex *sync.Mutex
		// Channel for confirming received menu
		menuReadyChan chan struct{}
	}
)

var (
	base   string
	port   string
	prompt = "GoBank@ATM> "
)

func init() {
	flag.StringVar(&base, "address", "localhost", "The base address to start the server on")
	flag.StringVar(&port, "port", "3000", "The port to start the server on")
	flag.Parse()
	runtime.GOMAXPROCS(runtime.NumCPU())
}

func newClient() *Client {
	return &Client{
		inputCh:       make(chan string, 10),
		writeCh:       make(chan *Protocol.Message),
		loginCh:       make(chan *Protocol.Message),
		balanceCh:     make(chan *Protocol.Message),
		depositCh:     make(chan *Protocol.Message),
		withdrawCh:    make(chan *Protocol.Message),
		menuReadyChan: make(chan struct{}),
		menu:          new(Protocol.MenuData),
		mutex:         new(sync.Mutex),
	}
}

func main() {
	client := newClient()
	// Base:Port
	address := net.JoinHostPort(base, port)
	// Dial server and get connection. 30s timeout is set.
	conn, err := net.DialTimeout("tcp", address, 30*time.Second)
	if err != nil {
		log.Fatalln(err)
	}

	//For UNIX signal handling.
	c := make(chan os.Signal)      //A channel to listen on keyboard events.
	signal.Notify(c, os.Interrupt) //If user pressed CTRL - C.
	go cleanUp(c, conn)

	log.Printf("You are connected to the server at %s", address)
	// Read from STDIN all the time.
	go func() {
		reader := bufio.NewReader(os.Stdin)
		for {
			line, _ := reader.ReadString('\n')
			line = strings.TrimSpace(line)
			if line == "" {
				fmt.Print(prompt)
				continue
			}
			client.inputCh <- line
		}
	}()
	client.start(conn)
	fmt.Println("Server disconnected")
}

func (c *Client) handleUserInput() {
	<-c.menuReadyChan // Wait for menu data to come.
	c.handleUser()
}

func (c *Client) handleUser() {
	var menu Protocol.Language
	//	Print out all the available languages
	for _, language := range c.menu.Languages {
		fmt.Println(language)
	}

	for {
		var ok bool
		fmt.Print(prompt)
		language := <-c.inputCh
		menu, ok = c.menu.Text[language]
		if !ok {
			fmt.Println("invalid language")
			continue
		}
		break
	}

	for {
		if err := c.login(&menu); err != nil {
			fmt.Println(err)
			continue
		} else {
			fmt.Println(menu.Title + "\n")
			fmt.Println(trimToLine(menu.Banner) + "\n")
			fmt.Println("1)", menu.InitialCommands.Balance)
			fmt.Println("2)", menu.InitialCommands.Deposit)
			fmt.Println("3)", menu.InitialCommands.Widthdraw)
			fmt.Println("4)", menu.InitialCommands.Logout)
			break
		}
	}

	// 	1) Kontrollera saldo
	//  2) Stoppa in pengar
	//  3) Ta ut pengar
	//  4) Logga ut

	for {
		fmt.Print(prompt)
		switch <-c.inputCh {
		case "1":
			c.writeCh <- &Protocol.Message{Code: Protocol.Balancecode}
			response := <-c.balanceCh
			switch response.Code {
			case Protocol.BalanceResponseCode:
				fmt.Printf("%s: %d\n", menu.Responses.Balance, response.Payload)
			case Protocol.BalanceResponseErrorCode:
				fmt.Println(menu.Errors.Balance)
			}

		case "2":
			fmt.Print("=> ")
			moneyString := <-c.inputCh
			if !Protocol.MoneyTest.Match([]byte(moneyString)) {
				fmt.Println(menu.Errors.Deposit)
				continue
			}
			money, _ := strconv.ParseUint(moneyString, 10, 32)
			c.writeCh <- &Protocol.Message{Code: Protocol.Depositcode, Payload: uint32(money)}
			response := <-c.depositCh
			switch response.Code {
			case Protocol.DepositResponseCode:
				fmt.Printf("%s: %d\n", menu.Responses.Deposit, response.Payload)
			case Protocol.DepositResponseErrorCode:
				fmt.Println(menu.Errors.Deposit)
			}

		case "3":
			fmt.Print("=> ")
			moneyString := <-c.inputCh
			fmt.Print("PIN => ")
			scratchPin := <-c.inputCh
			if !Protocol.MoneyTest.Match([]byte(moneyString)) {
				fmt.Println(menu.Errors.Invalid_command)
				continue
			} else if !Protocol.ScratchTest.Match([]byte(scratchPin)) {
				fmt.Println(menu.Errors.Incorrect_Pin)
				continue
			}
			money, _ := strconv.ParseUint(moneyString, 10, 32)
			pin, _ := strconv.ParseUint(scratchPin, 10, 16)
			c.writeCh <- &Protocol.Message{Code: Protocol.Withdrawcode, Number: uint16(pin), Payload: uint32(money)}
			response := <-c.withdrawCh
			switch response.Code {
			case Protocol.WithdrawResponseCode:
				fmt.Printf("%s: %d\n", menu.Responses.Withdraw, response.Payload)
			case Protocol.WithdrawResponseErrorCode:
				fmt.Println(menu.Errors.Withdraw)
			}
		case "4":
			c.writeCh <- &Protocol.Message{Code: Protocol.Logoutcode}
			c.handleUser()
			return
		default:
			fmt.Println(menu.Errors.Invalid_command)
		}
	}
}

// Start the basic client services.
func (c *Client) start(conn net.Conn) {
	defer conn.Close()
	go c.write(conn)
	c.writeCh <- &Protocol.Message{Code: Protocol.RequestMenucode}
	go c.handleUserInput()
	c.read(conn)
}

func (c *Client) login(menu *Protocol.Language) error {
	var cardNum, passNum string
	for {
		fmt.Println(menu.Interactions.Cardnumber)
		fmt.Print(prompt)
		cardNum = <-c.inputCh
		fmt.Println(menu.Interactions.Password)
		fmt.Print(prompt)
		passNum = <-c.inputCh
		if Protocol.CardnumberTest.MatchString(cardNum) {
			break
		} else {
			fmt.Println(menu.Errors.Login_error)
		}
	}

	//	Already tested through regex
	card, _ := strconv.Atoi(cardNum)
	pass, _ := strconv.Atoi(passNum)

	c.writeCh <- &Protocol.Message{
		Code:    Protocol.LoginCode,
		Number:  uint16(card),
		Payload: uint32(pass),
	}
	response := <-c.loginCh
	if response.Code == Protocol.LoginResponseError {
		return errors.New(menu.Errors.Login_error)
	}
	return nil
}

// Start listening on messages from server.
func (c *Client) read(conn net.Conn) {
	reader := bufio.NewReader(conn)
	var menu_data bytes.Buffer
	for {
		code, err := reader.Peek(1)
		if err != nil {
			return // Server disconnected
		}
		// log.Printf("Message code:%d", code[0])
		// Check message code
		switch code[0] {

		case Protocol.BalanceResponseErrorCode:
			fallthrough

		case Protocol.BalanceResponseCode:
			message := new(Protocol.Message)
			err := binary.Read(reader, binary.LittleEndian, message)
			if err != nil {
				log.Println(err)
				return
			}
			c.balanceCh <- message

		case Protocol.DepositResponseErrorCode:
			fallthrough

		case Protocol.DepositResponseCode:
			message := new(Protocol.Message)
			err := binary.Read(reader, binary.LittleEndian, message)
			if err != nil {
				log.Println(err)
				return
			}
			c.depositCh <- message

		case Protocol.WithdrawResponseErrorCode:
			fallthrough

		case Protocol.WithdrawResponseCode:
			message := new(Protocol.Message)
			err := binary.Read(reader, binary.LittleEndian, message)
			if err != nil {
				log.Println(err)
				return
			}
			c.withdrawCh <- message

		case Protocol.NewMenucode:
			message := new(Protocol.Message)
			err := binary.Read(reader, binary.LittleEndian, message)
			if err != nil {
				log.Println(err)
				return
			}
			c.writeCh <- &Protocol.Message{Code: Protocol.RequestMenucode}
			go func() {
				<-c.menuReadyChan // Wait for menu data to come.
			}()

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
				// Notify system that a new menu has arrived.
				c.menuReadyChan <- struct{}{}
			} else {
				menu_data.Write(menu_buffer[1:size])
			}

		case Protocol.LoginResponseError:
			reader.Read(make([]byte, 10))
			c.loginCh <- &Protocol.Message{Code: Protocol.LoginResponseError}

		case Protocol.LoginResponseOK:
			reader.Read(make([]byte, 10))
			c.loginCh <- &Protocol.Message{Code: Protocol.LoginResponseOK}

		default:
			log.Println("Unknown message code")
		}
	}
}

func (c *Client) write(conn net.Conn) {
	for {
		select {
		case message := <-c.writeCh:
			if err := binary.Write(conn, binary.LittleEndian, message); err != nil {
				log.Println(err)
				return
			}
		}
	}
}

// Adds a new menu to the system.
func (c *Client) addMenu(menu *Protocol.MenuData) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.menu = menu
}

func trimToLine(str string) string {
	if len(str) > 80 {
		return str[:80]
	} else {
		return str
	}
}

func cleanUp(c chan os.Signal, conn net.Conn) {
	<-c
	conn.Close() //close connection.
	fmt.Fprintln(os.Stderr, "\nThank you for using a ATM from Unicorn INC")
	os.Exit(1)
}
