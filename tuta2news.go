package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/net/proxy"
)

const (
	server         = "news.tcpreset.net:119"
	torProxy       = "127.0.0.1:9050"
	maxArticleSize = 32 * 1024
)

func main() {
	if err := processAndPostUsenetArticle(os.Stdin); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func processAndPostUsenetArticle(reader io.Reader) error {
	var usenetHeaderBuf bytes.Buffer
	var usenetBodyBuf bytes.Buffer
	var inUsenetBody = false
	var inEmailBody = false
	var articleSize int
	scanner := bufio.NewScanner(reader)
	foundFrom := false

	for scanner.Scan() {
		line := scanner.Text()
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			inEmailBody = true
			break
		}
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		return fmt.Errorf("error reading email headers: %v", err)
	}
	if !inEmailBody {
		return fmt.Errorf("no email body found (missing blank line after headers)")
	}

	for scanner.Scan() {
		line := scanner.Text()
		line = strings.TrimSuffix(line, "\r")
		if !inUsenetBody {
			if line == "" {
				inUsenetBody = true
				usenetHeaderBuf.WriteString("\r\n")
			} else {
				usenetHeaderBuf.WriteString(line)
				usenetHeaderBuf.WriteString("\r\n")
				if strings.HasPrefix(line, "From:") || strings.HasPrefix(line, "From ") {
					foundFrom = true
				}
			}
			continue
		}

		articleSize += len(line) + 1
		if articleSize > maxArticleSize {
			return fmt.Errorf("article size exceeds %d KB", maxArticleSize/1024)
		}
		usenetBodyBuf.WriteString(line)
		usenetBodyBuf.WriteString("\r\n")
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		return fmt.Errorf("error reading usenet article: %v", err)
	}

	if !foundFrom {
		return fmt.Errorf("missing required From: header in the Usenet article")
	}

	rawUsenetArticle := usenetHeaderBuf.String() + usenetBodyBuf.String()
	return postRawArticle(rawUsenetArticle)
}

func postRawArticle(rawArticle string) error {
	dialer, err := proxy.SOCKS5("tcp", torProxy, nil, proxy.Direct)
	if err != nil {
		return fmt.Errorf("error creating SOCKS5 dialer: %v", err)
	}

	conn, err := dialer.Dial("tcp", server)
	if err != nil {
		return fmt.Errorf("error connecting to the server through Tor: %v", err)
	}
	defer conn.Close()

	writer := &normalizedWriter{conn: conn}
	bufReader := bufio.NewReader(conn)

	serverGreeting, err := bufReader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("error reading server greeting: %v", err)
	}
	fmt.Print(serverGreeting)

	fmt.Fprint(writer, "POST\r\n")
	response, err := bufReader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("error sending POST command: %v", err)
	}
	fmt.Print(response)

	if strings.HasPrefix(response, "340") {
		fmt.Fprint(writer, rawArticle)
		fmt.Fprint(writer, ".\r\n")
		response, err = bufReader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("error sending raw article: %v", err)
		}
		fmt.Print(response)

		if !strings.HasPrefix(response, "240") {
			return fmt.Errorf("article posting failed: %s", response)
		}
	} else {
		return fmt.Errorf("server did not accept POST command: %s", response)
	}

	fmt.Fprint(writer, "QUIT\r\n")
	return nil
}

type normalizedWriter struct {
	conn io.Writer
}

func (w *normalizedWriter) Write(p []byte) (n int, err error) {
	text := string(p)
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\n", "\r\n")
	return w.conn.Write([]byte(text))
}