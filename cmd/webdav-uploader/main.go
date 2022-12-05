package main

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"github.com/studio-b12/gowebdav"
)

func main() {
	c, err := connectWebdav()
	if err != nil {
		return
	}
	localPath := "."
	remotePath := path.Join("share", "webdav")
	err = uploadToDav(localPath, c, remotePath)
	if err != nil {
		fmt.Printf("err: %v\n", err)
		return
	}
	fmt.Printf("ok")
}

func uploadToDav(localPath string, c *gowebdav.Client, remotePath string) error {
	return filepath.Walk(localPath, func(p string, info fs.FileInfo, err error) error {
		if info.IsDir() {
			if info.Name() == "." {
				return nil
			}
			err := makeDir(p, c, remotePath)
			if err != nil {
				return err
			}
		} else {
			err := uploadFile(p, c, remotePath)
			if err != nil {
				return err
			}
		}
		return nil
	})
}

func makeDir(p string, c *gowebdav.Client, remotePath string) error {
	err := c.Mkdir(path.Join(remotePath, p), 0644)
	if err != nil {
		return err
	}
	return nil
}

func uploadFile(p string, c *gowebdav.Client, remotePath string) error {
	file, err := os.Open(p)
	defer file.Close()
	if err != nil {
		return err
	}
	err = c.WriteStream(path.Join(remotePath, p), file, 0644)
	if err != nil {
		return err
	}
	return nil
}

func connectWebdav() (*gowebdav.Client, error) {
	webDavUrl := os.Getenv("WEBDAV_URL")
	user := os.Getenv("DAV_USER")
	password := os.Getenv("DAV_PWD")
	c := gowebdav.NewClient(webDavUrl, user, password)
	err := c.Connect()
	if err != nil {
		return nil, err
	}
	return c, nil
}
