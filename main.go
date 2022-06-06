package main

import (
	"archive/zip"
	"bufio"
	"encoding/csv"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/text/encoding/japanese"
)

const name = "aozoraGetter"
const indexFile = "list_person_all_extended_utf8.csv"

type Error struct {
	Error     error
	ErrorCode int
}

func isFile(filename string) bool {
	_, err := os.OpenFile(filename, os.O_RDONLY, 0)
	return !os.IsNotExist(err)
}

func Unzip(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		modTime := f.Modified

		os.MkdirAll(dest, 0755)

		rc, err := f.Open()
		if err != nil {
			return err
		}
		defer rc.Close()

		path := filepath.Join(dest, f.Name)
		if f.FileInfo().IsDir() {
			os.MkdirAll(path, f.Mode())
		} else {
			f, err := os.OpenFile(
				path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
			if err != nil {
				return err
			}
			defer f.Close()

			_, err = io.Copy(f, rc)
			if err != nil {
				return err
			}

			err = os.Chtimes(path, modTime, modTime)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func downloadFile(path, url string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

func getIndexFile(baseDir string) *Error {
	const (
		errorCode   = 201
		successCode = 200
	)

	indexZip := filepath.Join(baseDir, "tmp.zip")

	if !isFile(filepath.Join(baseDir, indexFile)) {

		if err := downloadFile(indexZip, "https://www.aozora.gr.jp/index_pages/list_person_all_extended_utf8.zip"); err != nil {
			return &Error{err, errorCode}
		}

		Unzip(indexZip, baseDir)
		if err := os.RemoveAll(indexZip); err != nil {
			return &Error{err, errorCode}
		}

		if err := os.RemoveAll(indexZip); err != nil {
			return &Error{err, errorCode}
		}

		return &Error{nil, successCode}
	}

	return nil
}

func loadCSV(path string, delim rune) ([][]string, error) {

	s, err := ioutil.ReadFile(path)
	if err != nil {
		return [][]string{}, err
	}

	r := csv.NewReader(strings.NewReader(string(s)))
	r.Comma = delim

	result, err := r.ReadAll()
	if err != nil {
		return [][]string{}, err
	}

	return result, nil
}

func curlBookFile(url, path string) (string, error) {
	tmpPath := filepath.Join(path, "tmp")
	if err := downloadFile(tmpPath, url); err != nil {
		return "", err
	}

	fp, err := os.Open(tmpPath)
	if err != nil {
		return "", err
	}
	defer fp.Close()

	decoder := japanese.ShiftJIS.NewDecoder()
	reader := bufio.NewReader(decoder.Reader(fp))
	result := ""
	for {
		line, _, err := reader.ReadLine()
		if err == io.EOF {
			break
		} else if err != nil {
			return "", err
		}
		result += string(line) + "\n"
	}

	return result, nil
}

func main() {
	/*
		終了コード一覧
		101: その他事前処理中のエラー
		200: インデックスダウンロード成功
		201: インデックスダウンロード失敗
		301: インデックス読み込み失敗
		401: 入力に部分一致する作品が見つからなかった
		501: 作品ファイルの取得に失敗
	*/

	execFile, err := os.Executable()
	if err != nil {
		fmt.Println(101)
		return
	}

	baseDir := filepath.Dir(execFile)

	if err := os.MkdirAll(baseDir, 0755); err != nil {
		fmt.Println(101)
	}

	if err := getIndexFile(baseDir); err != nil {
		fmt.Println(err.ErrorCode)
		return
	}

	if len(os.Args) < 2 {
		fmt.Println(101)
		return
	}

	titleQuery := os.Args[1]

	indexData, err := loadCSV(filepath.Join(baseDir, indexFile), ',')
	if err != nil {
		fmt.Println(301)
		return
	}

	bookUrl := ""

	for _, bookInfo := range indexData {
		title := bookInfo[1]
		if strings.Contains(title, titleQuery) {
			// aozorahackにtxtファイルがあるので取ってくる
			rawURL := bookInfo[45]
			preIndex := strings.Index(rawURL, "/card")
			sufIndex := strings.LastIndex(rawURL, ".zip")
			fileName := rawURL[strings.LastIndex(rawURL, "/")+1 : sufIndex]
			bookUrl = "https://aozorahack.org/aozorabunko_text" + rawURL[preIndex:sufIndex] + "/" + fileName + ".txt"
			break
		}
	}

	if bookUrl == "" {
		fmt.Println(401)
		return
	}

	book, err := curlBookFile(bookUrl, baseDir)
	if err != nil {
		fmt.Println(501)
		return
	}

	fmt.Println(book)

}
