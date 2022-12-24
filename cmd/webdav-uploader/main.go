package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"github.com/studio-b12/gowebdav"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

func main() {

	http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	cfg := initCmd()
	if cfg == nil {
		os.Exit(1)
		return
	}
	client := &webDavClient{
		baseUrl: cfg.WebDavUrl,
		user:    cfg.User,
		pass:    cfg.Password,
	}
	err := client.New().Connect()
	if err != nil {
		fmt.Println("can not connect to webdav", err)
		os.Exit(1)
		return
	}

	err = uploadToDav(cfg.LocalPath, cfg.RemotePath, cfg.ExcludeRegex, client)
	if err != nil {
		fmt.Println("can not upload to webdav", err)
		os.Exit(1)
		return
	}
}

func initCmd() *Config {
	cfg := &Config{}
	flag.StringVar(&cfg.LocalPath, "local", "", "local path")
	flag.StringVar(&cfg.RemotePath, "remote", "", "remote path")
	flag.StringVar(&cfg.WebDavUrl, "url", "", "webdav url")
	flag.StringVar(&cfg.User, "user", "", "user")
	flag.StringVar(&cfg.Password, "pwd", "", "password")
	flag.StringVar(&cfg.ExcludeRegex, "exclude", "", "exclude path regex")
	flag.Parse()
	if cfg.LocalPath == "" {
		fmt.Println("local path is empty")
		return nil
	}
	if cfg.RemotePath == "" {
		fmt.Println("remote path is empty")
		return nil
	}
	if cfg.WebDavUrl == "" {
		fmt.Println("webdav url is empty")
		return nil
	}
	if cfg.User == "" {
		fmt.Println("user is empty")
		return nil
	}
	if cfg.Password == "" {
		fmt.Println("password is empty")
		return nil
	}
	if cfg.ExcludeRegex != "" {
		_, err := regexp.Compile(cfg.ExcludeRegex)
		if err != nil {
			fmt.Println("exclude regex is invalid")
			return nil
		}
	}
	return cfg
}

type Config struct {
	WebDavUrl    string
	User         string
	Password     string
	LocalPath    string
	RemotePath   string
	ExcludeRegex string
}

func uploadToDav(localPath string, remotePath string, excludeRegex string, client *webDavClient) error {
	localPath = filepath.Clean(localPath)

	stat, err := os.Stat(localPath)
	if err != nil {
		return err
	}

	if stat.IsDir() {
		return filepath.Walk(localPath, func(p string, info fs.FileInfo, err error) error {
			if p[len(localPath):] == "" {
				return nil
			}
			relativePath := p[len(localPath)+1:]
			if localPath == "." {
				relativePath = p
			}

			if match, _ := regexp.Match(excludeRegex, []byte(relativePath)); len(excludeRegex) != 0 && match {
				fmt.Println("exclude:", relativePath)
				return nil
			}

			if info.IsDir() {
				err := makeDir(client, urlPathJoin(remotePath, relativePath))
				if err != nil {
					return err
				}
			} else {
				err := uploadFile(client, p, urlPathJoin(remotePath, relativePath))
				if err != nil {
					return err
				}
			}
			return nil
		})
	} else {
		fileName := localPath[len(filepath.Dir(localPath))+1:]
		return uploadFile(client, localPath, urlPathJoin(remotePath, fileName))
	}
}

func urlPathJoin(p ...string) string {
	s := filepath.Join(p...)
	if runtime.GOOS == "windows" {
		return strings.ReplaceAll(s, "\\", "/")
	} else {
		return s
	}
}

func makeDir(client *webDavClient, remotePath string) error {
	c := client.New()
	err := c.Mkdir(remotePath, 0644)
	if err != nil {
		return err
	}
	return nil
}

func uploadFile(client *webDavClient, p string, remotePath string) error {
	file, err := os.Open(p)
	if err != nil {
		return err
	}
	defer file.Close()

	readerChan := make(chan fileChunk)
	var wg sync.WaitGroup

	go func() {
		err := readFile(file, readerChan)
		close(readerChan)
		if err != nil {
			fmt.Printf("read file filed, err: %v\n", err)
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	startTime := time.Now()
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go uploadFileSplit(&wg, readerChan, client, remotePath, ctx, cancel)
	}

	wg.Wait()
	if err := ctx.Err(); err != nil {
		c := client.New()
		err := c.Remove(remotePath)
		if err != nil {
			return err
		}
		return err
	}
	endTime := time.Now()
	fileStat, _ := file.Stat()
	// calculate the speed in MB/s
	speed := float64(fileStat.Size()) / 1024.0 / 1024.0 / endTime.Sub(startTime).Seconds()
	fmt.Printf("upload file %s to %s, avg speed: %f MB/s\n", p, remotePath, speed)
	return nil
}

func readFile(f *os.File, readerChan chan fileChunk) error {
	fi, err := f.Stat()
	if err != nil {
		return err
	}

	maxReadSize := int64(64 * 1024 * 1024)

	// read file to channel
	for i := int64(0); ; i++ {
		offset := i * maxReadSize
		if offset >= fi.Size() {
			break
		}
		readLen := maxReadSize
		needRead := fi.Size() - offset
		if needRead < maxReadSize {
			readLen = needRead
		}
		readerChan <- fileChunk{
			Index:  i,
			Offset: offset,
			Length: readLen,
			Size:   fi.Size(),
			Reader: io.NewSectionReader(f, offset, readLen),
		}
	}
	return nil
}

func uploadFileSplit(
	wg *sync.WaitGroup,
	readerChan <-chan fileChunk,
	client *webDavClient,
	remotePath string,
	ctx context.Context,
	cancel context.CancelFunc,
) {
	defer wg.Done()
	for chunk := range readerChan {

		select {
		case <-ctx.Done():
			return
		default:
		}

		retryMax := 10
		startTime := time.Now()
		success := false
		for retry := 0; retry < retryMax; retry++ {
			c := client.New()
			c.SetHeader("Content-Range", fmt.Sprintf("bytes %d-%d/%d",
				chunk.Offset, chunk.Offset+chunk.Length-1, chunk.Size))
			err := c.WriteStream(remotePath, chunk.Reader, 0644)
			if err != nil {
				continue
			} else {
				success = true
				endTime := time.Now()
				// calculate speed in MB/s
				speed := float64(chunk.Length) / 1024 / 1024 / endTime.Sub(startTime).Seconds()
				fmt.Printf("Index: %d, chunk: %d %d %d uploaded, speed: %f MB/s\n",
					chunk.Index, chunk.Offset, chunk.Offset+chunk.Length, chunk.Size, speed)
				break
			}
		}
		if !success {
			fmt.Printf("Index: %d, chunk: %d %d %d upload failed\n",
				chunk.Index, chunk.Offset, chunk.Offset+chunk.Length, chunk.Size)
			cancel()
		}
	}
}

type fileChunk struct {
	Index  int64
	Offset int64
	Length int64
	Size   int64
	Reader io.Reader
}

type webDavClient struct {
	baseUrl string
	user    string
	pass    string
}

func (w *webDavClient) New() *gowebdav.Client {
	return gowebdav.NewClient(w.baseUrl, w.user, w.pass)
}
