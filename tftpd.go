// written by claude, public domain
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	bufferSize = 516 // TFTP block size (512) + 4 bytes for opcode and block number
)

type opcode uint16

const (
	opRRQ  opcode = 1 // Read request
	opWRQ  opcode = 2 // Write request
	opDATA opcode = 3 // Data
	opACK  opcode = 4 // Acknowledgement
	opERR  opcode = 5 // Error
)

var rootDir string

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	flag.StringVar(&rootDir, "root", "", "Root directory for TFTP files (mandatory)")
	flag.Parse()

	if rootDir == "" {
		log.Fatal("Error: Root directory must be specified using the -root flag")
	}

	absRootDir, err := filepath.Abs(rootDir)
	if err != nil {
		log.Fatalf("Error resolving root directory: %v", err)
	}
	rootDir = filepath.Clean(absRootDir)

	if _, err := os.Stat(rootDir); os.IsNotExist(err) {
		log.Fatalf("Error: Root directory does not exist: %s", rootDir)
	}

	log.Printf("Using root directory: %s", rootDir)

	addr, err := net.ResolveUDPAddr("udp", ":69") // TFTP uses port 69 by default
	if err != nil {
		log.Fatal(err)
	}

	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	log.Printf("TFTP server listening on %s", conn.LocalAddr())

	for {
		buffer := make([]byte, bufferSize)
		n, remoteAddr, err := conn.ReadFromUDP(buffer)
		if err != nil {
			log.Printf("Error reading from UDP: %v", err)
			continue
		}

		log.Printf("Received %d bytes from %s", n, remoteAddr)

		go handleRequest(buffer[:n], remoteAddr)
	}
}

func handleRequest(request []byte, remoteAddr *net.UDPAddr) {
	opcode := opcode(uint16(request[0])<<8 | uint16(request[1]))

	log.Printf("Handling request from %s, opcode: %d", remoteAddr, opcode)

	switch opcode {
	case opRRQ:
		handleReadRequest(request[2:], remoteAddr)
	case opWRQ:
		handleWriteRequest(request[2:], remoteAddr)
	default:
		sendError(remoteAddr, 4, "Unsupported TFTP opcode")
	}
}

func handleReadRequest(request []byte, remoteAddr *net.UDPAddr) {
	filename, mode, err := parseRequest(request)
	if err != nil {
		log.Printf("Error parsing request: %v", err)
		sendError(remoteAddr, 0, "Invalid request")
		return
	}

	log.Printf("Read request from %s for file: %s, mode: %s", remoteAddr, filename, mode)

	fullPath, err := resolveFilePath(filename)
	if err != nil {
		log.Printf("Invalid file path: %s", filename)
		sendError(remoteAddr, 2, "Access violation")
		return
	}

	file, err := os.Open(fullPath)
	if err != nil {
		log.Printf("File not found: %s", fullPath)
		sendError(remoteAddr, 1, "File not found")
		return
	}
	defer file.Close()

	conn, err := net.DialUDP("udp", nil, remoteAddr)
	if err != nil {
		log.Printf("Error creating UDP connection: %v", err)
		return
	}
	defer conn.Close()

	log.Printf("Established connection from %s to %s", conn.LocalAddr(), conn.RemoteAddr())

	buffer := make([]byte, 512)
	blockNum := uint16(1)
	totalBytesSent := 0

	for {
		n, err := file.Read(buffer)
		if err != nil && err != io.EOF {
			log.Printf("Error reading file: %v", err)
			sendError(remoteAddr, 0, "Error reading file")
			return
		}

		sendData(conn, blockNum, buffer[:n])
		log.Printf("Sent block %d (%d bytes) to %s", blockNum, n, remoteAddr)

		totalBytesSent += n

		if err := waitForAck(conn, blockNum); err != nil {
			log.Printf("Error waiting for ACK: %v", err)
			return
		}

		if n < 512 {
			log.Printf("Transfer complete for file: %s, Total bytes sent: %d", fullPath, totalBytesSent)
			break // Last block sent
		}

		blockNum++
	}
}

func handleWriteRequest(request []byte, remoteAddr *net.UDPAddr) {
	filename, mode, err := parseRequest(request)
	if err != nil {
		log.Printf("Error parsing request: %v", err)
		sendError(remoteAddr, 0, "Invalid request")
		return
	}

	log.Printf("Write request from %s for file: %s, mode: %s", remoteAddr, filename, mode)

	fullPath, err := resolveFilePath(filename)
	if err != nil {
		log.Printf("Invalid file path: %s", filename)
		sendError(remoteAddr, 2, "Access violation")
		return
	}

	file, err := os.Create(fullPath)
	if err != nil {
		log.Printf("Cannot create file: %s", fullPath)
		sendError(remoteAddr, 2, "Cannot create file")
		return
	}
	defer file.Close()

	conn, err := net.DialUDP("udp", nil, remoteAddr)
	if err != nil {
		log.Printf("Error creating UDP connection: %v", err)
		return
	}
	defer conn.Close()

	log.Printf("Established connection from %s to %s", conn.LocalAddr(), conn.RemoteAddr())

	blockNum := uint16(0)
	sendAck(conn, blockNum)

	totalBytesReceived := 0

	for {
		buffer := make([]byte, bufferSize)
		n, _, err := conn.ReadFromUDP(buffer)
		if err != nil {
			log.Printf("Error reading from UDP: %v", err)
			return
		}

		opcode := opcode(uint16(buffer[0])<<8 | uint16(buffer[1]))
		if opcode != opDATA {
			log.Printf("Expected DATA packet, got opcode: %d", opcode)
			sendError(remoteAddr, 4, "Expected DATA packet")
			return
		}

		blockNum = uint16(buffer[2])<<8 | uint16(buffer[3])
		data := buffer[4:n]

		log.Printf("Received block %d (%d bytes) from %s", blockNum, len(data), remoteAddr)

		_, err = file.Write(data)
		if err != nil {
			log.Printf("Error writing to file: %v", err)
			sendError(remoteAddr, 3, "Error writing to file")
			return
		}

		totalBytesReceived += len(data)

		sendAck(conn, blockNum)

		if len(data) < 512 {
			log.Printf("Transfer complete for file: %s, Total bytes received: %d", fullPath, totalBytesReceived)
			break // Last block received
		}
	}
}

func parseRequest(request []byte) (string, string, error) {
	parts := make([][]byte, 0, 2)
	start := 0
	for i, b := range request {
		if b == 0 {
			parts = append(parts, request[start:i])
			start = i + 1
			if len(parts) == 2 {
				break
			}
		}
	}

	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid request format")
	}

	filename := string(parts[0])
	mode := string(parts[1])

	return filename, mode, nil
}

func resolveFilePath(filename string) (string, error) {
	cleanPath := filepath.Clean(filename)
	if strings.HasPrefix(cleanPath, "..") {
		return "", fmt.Errorf("access denied")
	}
	fullPath := filepath.Join(rootDir, cleanPath)
	return fullPath, nil
}

func sendData(conn *net.UDPConn, blockNum uint16, data []byte) {
	packet := make([]byte, len(data)+4)
	packet[0] = 0
	packet[1] = byte(opDATA)
	packet[2] = byte(blockNum >> 8)
	packet[3] = byte(blockNum)
	copy(packet[4:], data)

	_, err := conn.Write(packet)
	if err != nil {
		log.Printf("Error sending DATA packet: %v", err)
	}
}

func sendAck(conn *net.UDPConn, blockNum uint16) {
	packet := []byte{0, byte(opACK), byte(blockNum >> 8), byte(blockNum)}
	_, err := conn.Write(packet)
	if err != nil {
		log.Printf("Error sending ACK packet: %v", err)
	} else {
		log.Printf("Sent ACK for block %d to %s", blockNum, conn.RemoteAddr())
	}
}

func sendError(addr *net.UDPAddr, errorCode uint16, errorMsg string) {
	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		log.Printf("Error creating UDP connection for error message: %v", err)
		return
	}
	defer conn.Close()

	packet := make([]byte, 4+len(errorMsg)+1)
	packet[0] = 0
	packet[1] = byte(opERR)
	packet[2] = byte(errorCode >> 8)
	packet[3] = byte(errorCode)
	copy(packet[4:], errorMsg)
	packet[len(packet)-1] = 0

	_, err = conn.Write(packet)
	if err != nil {
		log.Printf("Error sending ERROR packet: %v", err)
	} else {
		log.Printf("Sent ERROR (code: %d, message: %s) to %s", errorCode, errorMsg, addr)
	}
}

func waitForAck(conn *net.UDPConn, expectedBlock uint16) error {
	buffer := make([]byte, 4)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, _, err := conn.ReadFromUDP(buffer)
	if err != nil {
		return fmt.Errorf("error reading ACK: %v", err)
	}
	if n != 4 {
		return fmt.Errorf("invalid ACK packet size")
	}
	opcode := opcode(uint16(buffer[0])<<8 | uint16(buffer[1]))
	blockNum := uint16(buffer[2])<<8 | uint16(buffer[3])
	if opcode != opACK || blockNum != expectedBlock {
		return fmt.Errorf("unexpected ACK: opcode=%d, block=%d", opcode, blockNum)
	}
	return nil
}
