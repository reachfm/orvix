package pop3

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
)

func TestHardeningPOP3RepeatedConnectDisconnect(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping hardening test in short mode")
	}

	_, addr, cleanup := testPOP3Server(t)
	defer cleanup()

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := net.Dial("tcp", addr)
			if err != nil {
				return
			}
			defer conn.Close()
			reader := bufio.NewReader(conn)
			reader.ReadString('\n')
			fmt.Fprintf(conn, "USER user@test.com\r\n")
			reader.ReadString('\n')
			fmt.Fprintf(conn, "PASS pass\r\n")
			reader.ReadString('\n')
			fmt.Fprintf(conn, "STAT\r\n")
			reader.ReadString('\n')
			fmt.Fprintf(conn, "QUIT\r\n")
			reader.ReadString('\n')
		}()
	}
	wg.Wait()
}

func TestHardeningPOP3RepeatedRetr(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping hardening test in short mode")
	}

	_, addr, cleanup := testPOP3Server(t)
	defer cleanup()

	for i := 0; i < 30; i++ {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatalf("dial %d: %v", i, err)
		}
		reader := bufio.NewReader(conn)
		reader.ReadString('\n')
		fmt.Fprintf(conn, "USER user@test.com\r\n")
		reader.ReadString('\n')
		fmt.Fprintf(conn, "PASS pass\r\n")
		reader.ReadString('\n')
		fmt.Fprintf(conn, "RETR 1\r\n")
		for {
			line, _ := reader.ReadString('\n')
			if strings.TrimSpace(line) == "." {
				break
			}
		}
		fmt.Fprintf(conn, "QUIT\r\n")
		reader.ReadString('\n')
		conn.Close()
	}
}
