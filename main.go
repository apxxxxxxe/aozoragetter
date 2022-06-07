package main

import (
	"archive/zip"
	"bufio"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/unicode/norm"
)

const name = "aozoraGetter"
const indexFile = "list_person_all_extended_utf8.csv"

const zenkakuByte = 3

var (
	errIsNotValidBook = errors.New("error: the book is not valid")
)

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

		if err := Unzip(indexZip, baseDir); err != nil {
			return &Error{err, errorCode}
		}

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

func curlBookFile(url, path string) (string, string, error) {
	tmpPath := filepath.Join(path, "tmp")
	if err := downloadFile(tmpPath, url); err != nil {
		return "", tmpPath, err
	}

	fp, err := os.Open(tmpPath)
	if err != nil {
		return "", tmpPath, err
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
			return "", tmpPath, err
		}
		result += string(line) + "\n"
	}

	return result, tmpPath, nil
}

func getBookURL(data []string) (string, error) {
	rawURL := data[45]
	preIndex := strings.Index(rawURL, "/card")
	sufIndex := strings.LastIndex(rawURL, ".zip")
	if preIndex == -1 || sufIndex == -1 {
		return "", errIsNotValidBook
	}
	fileName := rawURL[strings.LastIndex(rawURL, "/")+1 : sufIndex]
	return "https://aozorahack.org/aozorabunko_text" + rawURL[preIndex:sufIndex] + "/" + fileName + ".txt", nil
}

func getInfoSummury(bookInfo []string) map[string]string {
	result := map[string]string{}
	result["title"] = bookInfo[1]
	result["author"] = bookInfo[15] + bookInfo[16]
	result["teihon"] = bookInfo[27]
	return result
}

func searchBook(query string, indexData [][]string) (string, [][]string) {
	bookUrl := ""
	candidates := [][]string{}

	for _, bookInfo := range indexData {
		infoSummury := getInfoSummury(bookInfo)
		if strings.Contains(infoSummury["title"], query) || strings.Contains(infoSummury["author"], query) {
			candidates = append(candidates, bookInfo)
			if len(candidates) == 1 {
				// aozorahackにtxtファイルがあるので取ってくる
				var err error
				bookUrl, err = getBookURL(bookInfo)
				if err != nil {
					candidates = [][]string{}
				}
			}
		}
	}

	return bookUrl, candidates
}

func main() {
	/*
		終了コード一覧
		101: その他事前処理中のエラー
		200: インデックスダウンロード成功
		201: インデックスダウンロード失敗
		301: インデックス読み込み失敗
		400: 部分一致する作品群が見つかった
		401: 入力に部分一致する作品が見つからなかった
		500: 一つの部分一致する作品群を返す(２行目から作品の本文が返る)
		501: 作品ファイルの取得に失敗
		0: 完全一致する作品が見つかった(２行目から作品の本文が返る)
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

	queryWords := os.Args[1:]

	indexData, err := loadCSV(filepath.Join(baseDir, indexFile), ',')
	if err != nil {
		fmt.Println(301)
		return
	}

	candidates := indexData
	bookUrl := ""
	for _, query := range queryWords {
		// クエリの数だけ繰り返し作品の絞り込み
		bookUrl, candidates = searchBook(query, candidates)
	}

	if len(candidates) > 1 {
		fmt.Println(400)
		for _, c := range candidates {
			s := getInfoSummury(c)
			fmt.Println("「" + s["title"] + "」" + s["author"] + "(" + s["teihon"] + ")")
		}
		return
	} else if bookUrl == "" {
		fmt.Println(401)
		return
	}

	book, bookPath, err := curlBookFile(bookUrl, baseDir)
	if err != nil {
		fmt.Println(501)
		return
	}

	if err := os.RemoveAll(bookPath); err != nil {
		fmt.Println(101)
		return
	}

	processedBook := ""
	lines := strings.Split(book, "\n")
	jisage := ""
	singleJisage := ""
	jiageCount := 0
	i := 0
	for i < len(lines) {

		if strings.HasPrefix(lines[i], "----------") {
			i++
			for !strings.HasPrefix(lines[i], "----------") {
				i++
			}
			i++
		}

		if strings.Contains(lines[i], "改ページ］") {
			lines[i] = "■□" + lines[i]
		}

		if strings.HasPrefix(lines[i], "［＃ここから") {
			pos := strings.IndexAny(lines[i], "０１２３４５６７８９")
			jisageCount, err := strconv.Atoi(string(norm.NFKC.Bytes([]byte(lines[i][pos : pos+3]))))
			if err != nil {
				panic(err)
			}
			jisage = strings.Repeat("　", jisageCount)
			i++
		}

		if strings.HasPrefix(lines[i], "［＃ここで字下げ終わり］") {
			jisage = ""
			i++
		}

		if strings.Contains(lines[i], "字下げ］") {
			pos := strings.IndexAny(lines[i], "０１２３４５６７８９")
			jisageCount, err := strconv.Atoi(string(norm.NFKC.Bytes([]byte(lines[i][pos : pos+3]))))
			if err != nil {
				panic(err)
			}
			singleJisage = strings.Repeat("　", jisageCount)
		}

		if strings.Contains(lines[i], "［＃地から") && strings.Contains(lines[i], "字上げ］") {
			pos := strings.IndexAny(lines[i], "０１２３４５６７８９")
			var err error
			jiageCount, err = strconv.Atoi(string(norm.NFKC.Bytes([]byte(lines[i][pos : pos+3]))))
			if err != nil {
				panic(err)
			}
			jiageCount *= zenkakuByte
		}

		if strings.HasPrefix(lines[i], "底本：") {
			break
		}

		if strings.Contains(lines[i], "は大見出し］") {
			lines[i] = "◆◇" + lines[i]
		}

		if strings.Contains(lines[i], "は中見出し］") {
			lines[i] = "◇◆" + lines[i]
		}

		if strings.Contains(lines[i], "※［") {
			rep := regexp.MustCompile(`※［[^］]*］`)
			lines[i] = rep.ReplaceAllString(lines[i], `★`)
		}

		/*
			if strings.Contains(lines[i], "［") {
				rep := regexp.MustCompile(`［[^］]*］`)
				lines[i] = rep.ReplaceAllString(lines[i], "")
			}
		*/

		line := singleJisage + jisage + lines[i] + "\n"
		processedBook += line[jiageCount:]
		singleJisage = ""
		jiageCount = 0
		i++

	}

	fmt.Println(0)
	fmt.Println(processedBook)

}
