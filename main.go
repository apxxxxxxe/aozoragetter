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
	"unicode"

	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/unicode/norm"

	"github.com/ikawaha/kagome-dict/ipa"
	"github.com/ikawaha/kagome/v2/tokenizer"
)

const name = "aozoragetter"
const indexFile = "list_person_all_extended_utf8.csv"

const zenkakuByte = 3

var (
	errIsNotValidBook = errors.New("error: the book is not valid")
	rep               = map[string]*regexp.Regexp{
		"bousen":      regexp.MustCompile(`［＃「(.*?)」(の左)?に((白ゴマ|丸|白丸|黒三角|白三角|二重丸|蛇の目)?傍点|((二重)?傍|鎖|破|波)線)］`),
		"bousenStart": regexp.MustCompile(`［＃(左に)?((白ゴマ|丸|白丸|黒三角|白三角|二重丸|蛇の目)?傍点|((二重)?傍|鎖|破|波)線)］`),
		"bousenEnd":   regexp.MustCompile(`［＃(左に)?((白ゴマ|丸|白丸|黒三角|白三角|二重丸|蛇の目)?傍点|((二重)?傍|鎖|破|波)線)終わり］`),
		"futoji":      regexp.MustCompile(`［＃「(.*?)」は太字］`),
		"futojiStart": regexp.MustCompile(`［＃(ここから)?太字］`),
		"futojiEnd":   regexp.MustCompile(`［＃(ここで)?太字終わり］`),
		"shatai":      regexp.MustCompile(`［＃「(.*?)」は斜体］`),
		"shataiStart": regexp.MustCompile(`［＃(ここから)?斜体］`),
		"shataiEnd":   regexp.MustCompile(`［＃(ここで)?斜体終わり］`),
		"jisageStart": regexp.MustCompile(`［＃(ここから)?([０１２３４５６７８９]+)字下げ］`),
	}
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

func curlBookFile(url, path string) (string, error) {
	tmpPath := filepath.Join(path, "tmp")
	if err := downloadFile(tmpPath, url); err != nil {
		return "", err
	}

	fp, err := os.Open(tmpPath)
	if err != nil {
		return "", err
	}
	defer func() {
		fp.Close()
		os.RemoveAll(tmpPath)
	}()

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
	return result
}

func searchBook(query string, indexData [][]string) (string, [][]string) {
	bookUrl := ""
	candidates := [][]string{}

	for _, bookInfo := range indexData {
		infoSummury := getInfoSummury(bookInfo)
		if strings.Contains(infoSummury["title"], query) || strings.Contains(infoSummury["author"], query) {
			isUniqueBook := true
			for _, c := range candidates {
				s := getInfoSummury(c)
				if infoSummury["title"] == s["title"] && infoSummury["author"] == s["author"] {
					// 同作品名、同作者名は同一作品とみなす
					isUniqueBook = false
				}
			}
			if isUniqueBook {
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
	}

	return bookUrl, candidates
}

// http://kumihan.aozora.gr.jp/slabid-19.htmに記載されている注記を処理する
func formatText(book string) string {

	processRuby := func(book string) string {

		isKanji := func(src string) bool {
			result := true
			for _, r := range src {
				if !unicode.In(rune(r), unicode.Han) {
					result = false
				}
			}
			return result
		}

		t, err := tokenizer.New(ipa.Dict(), tokenizer.OmitBosEos())
		if err != nil {
			panic(err)
		}
		seg := t.Wakati(book)

		j := 0
		for j < len(seg) {

			if seg[j] == "｜" {
				seg[j] = "《"
				j++
				for seg[j] != "《" {
					j++
				}
				seg[j] = ","
			}

			if seg[j] == "《" {
				tango := []string{}
				j--
				for isKanji(seg[j]) {
					tango = append([]string{seg[j]}, tango...)
					j--
				}
				j++
				seg[j] = "《"
				for _, t := range tango {
					j++
					seg[j] = t
				}

				seg = append(seg, "")
				copy(seg[j+2:], seg[j+1:])
				seg[j+1] = ","

				for seg[j] != "》" {
					j++
				}
			}

			j++
		}

		book = ""
		for _, s := range seg {
			book += s
		}

		return book
	}

	result := ""
	lines := strings.Split(book, "\n")
	jisage := ""
	jiage := ""
	align := ""
	singleJiage := ""
	singleJisage := ""
	singleAlign := ""
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

		if strings.Contains(lines[i], "［＃改丁］") {
			lines[i] = "\\x[noclear]\\c" + lines[i]
		}

		if strings.Contains(lines[i], "［＃改ページ］") || strings.Contains(lines[i], "［＃改段］") {
			lines[i] = "\\x[noclear]\\c" + lines[i]
		}

		// 「折り返して字下げ」と「改行天付き」はここで「ここから○字下げ」として扱う
		if strings.Contains(lines[i], "字下げ］") {
			subMatch := rep["jisageStart"].FindStringSubmatch(lines[i])

			jisageCount, err := strconv.Atoi(string(norm.NFKC.Bytes([]byte(subMatch[2]))))
			if err != nil {
				panic(err)
			}

			if subMatch[1] == "ここから" {
				// ［＃ここからn字下げ］の場合
				jisage += strings.Repeat("　", jisageCount)
			} else {
				// ［＃n字下げ］の場合
				singleJisage = strings.Repeat("　", jisageCount)
			}
		}

		if strings.Contains(lines[i], "［＃ここで字下げ終わり］") {
			jisage = ""
		}

		if strings.Contains(lines[i], "［＃ここから地付き］") {
			align = "\\f[align,right]"
		}

		if strings.Contains(lines[i], "［＃ここで地付き終わり］") {
			align = "\\f[align,left]"
		}

		if strings.HasPrefix(lines[i], "［＃ここから") && strings.Contains(lines[i], "字上げ") {
			pos := strings.IndexAny(lines[i], "０１２３４５６７８９")
			jiageCount, err := strconv.Atoi(string(norm.NFKC.Bytes([]byte(lines[i][pos : pos+3]))))
			if err != nil {
				panic(err)
			}
			jiage = strings.Repeat("　", jiageCount)
			i++
		}

		if strings.HasPrefix(lines[i], "［＃ここで字上げ終わり］") {
			jiage = ""
			i++
		}

		if strings.Contains(lines[i], "字上げ］") {
			pos := strings.IndexAny(lines[i], "０１２３４５６７８９")
			jiageCount, err := strconv.Atoi(string(norm.NFKC.Bytes([]byte(lines[i][pos : pos+3]))))
			if err != nil {
				panic(err)
			}
			singleJiage = strings.Repeat("　", jiageCount)
		}

		if strings.Contains(lines[i], "［＃地付き］") {
			singleAlign = "\\f[align,right]"
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

		//"［＃ページの左右中央］"

		if strings.Contains(lines[i], "見出し］") {
			lines[i] = "\\![bold,1]" + lines[i] + "\\![bold,0]"
		}

		if strings.Contains(lines[i], "※［") {
			rep := regexp.MustCompile(`※［[^］]*］`)
			lines[i] = rep.ReplaceAllString(lines[i], `⺀`)
		}

		/*
			if strings.Contains(lines[i], "［") {
				rep := regexp.MustCompile(`［[^］]*］`)
				lines[i] = rep.ReplaceAllString(lines[i], "")
			}
		*/

		for _, s := range rep["bousen"].FindAllStringSubmatch(lines[i], -1) {
			lines[i] = strings.Replace(lines[i], s[1], "\\![underline,1]"+s[1]+"\\![underline,0]", 1)
		}

		lines[i] = rep["bousenStart"].ReplaceAllString(lines[i], "\\![underline,1]")

		lines[i] = rep["bousenEnd"].ReplaceAllString(lines[i], "\\![underline,0]")

		for _, s := range rep["futoji"].FindAllStringSubmatch(lines[i], -1) {
			lines[i] = strings.Replace(lines[i], s[1], "\\![bold,1]"+s[1]+"\\![bold,0]", 1)
		}

		lines[i] = rep["futojiStart"].ReplaceAllString(lines[i], "\\![bold,1]")

		lines[i] = rep["futojiEnd"].ReplaceAllString(lines[i], "\\![bold,0]")

		for _, s := range rep["shatai"].FindAllStringSubmatch(lines[i], -1) {
			lines[i] = strings.Replace(lines[i], s[1], "\\![italic,1]"+s[1]+"\\![italic,0]", 1)
		}

		lines[i] = rep["shataiStart"].ReplaceAllString(lines[i], "\\![italic,1]")

		lines[i] = rep["shataiEnd"].ReplaceAllString(lines[i], "\\![italic,0]")

		line := singleAlign + align + singleJisage + jisage + lines[i] + singleJiage + jiage + "\n"
		if len(singleJisage)+len(jisage) > jiageCount {
			result += line[jiageCount:]
		} else {
			result += line
		}
		singleJisage = ""
		singleJiage = ""
		singleAlign = ""
		jiageCount = 0
		i++
	}

	return processRuby(result)
}

func main() {
	sub(os.Args)
}

func sub(args []string) {
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

	var execFile string
	if args[0] == "test" {
		var err error
		execFile, err = os.Getwd()
		if err != nil {
			fmt.Println(101)
			return
		}
	} else {
		var err error
		execFile, err = os.Executable()
		if err != nil {
			fmt.Println(101)
			return
		}
	}

	baseDir := filepath.Dir(execFile)

	if err := os.MkdirAll(baseDir, 0755); err != nil {
		fmt.Println(101)
	}

	if err := getIndexFile(baseDir); err != nil {
		fmt.Println(err.ErrorCode)
		return
	}

	if len(args) < 2 {
		fmt.Println(101)
		return
	}

	queryWords := args[1:]

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
			fmt.Println("「" + s["title"] + "」" + s["author"])
		}
		return
	} else if bookUrl == "" {
		fmt.Println(401)
		return
	}

	book, err := curlBookFile(bookUrl, baseDir)
	if err != nil {
		fmt.Println(501)
		return
	}

	processedBook := formatText(book)

	fmt.Println(0)
	fmt.Println(processedBook)

}
