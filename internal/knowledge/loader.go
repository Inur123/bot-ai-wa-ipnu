package knowledge

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/ledongthuc/pdf"
)

const defaultDir = "knowledge"
const defaultMaxChars = 12000
const minMaxChars = 2000

type knowledgeFile struct {
	name string
	data []byte
}

// Load membaca file knowledge dari sumber aktif dan mengembalikan teks gabungan untuk prompt AI.
func Load() string {
	return LoadDir(defaultDir)
}

// Search memilih potongan knowledge yang paling relevan dengan pertanyaan user.
func Search(query string) string {
	return SearchWithLimit(query, configuredMaxChars())
}

func SearchWithLimit(query string, maxChars int) string {
	return SearchDir(defaultDir, query, maxChars)
}

func LoadDir(dir string) string {
	var sb strings.Builder
	for _, file := range sourceFiles(dir) {
		content, err := readKnowledgeData(file.name, file.data)
		if err != nil || strings.TrimSpace(content) == "" {
			continue
		}

		sb.WriteString(fmt.Sprintf("\n--- FILE: %s ---\n", file.name))
		sb.WriteString(content)
		sb.WriteString("\n")
	}

	return sb.String()
}

func SearchDir(dir, query string, maxChars int) string {
	if maxChars <= 0 {
		maxChars = defaultMaxChars
	}
	if maxChars < minMaxChars {
		maxChars = minMaxChars
	}

	terms := queryTerms(query)
	var matches []scoredChunk
	var catalog []string

	for _, file := range sourceFiles(dir) {
		content, err := readKnowledgeData(file.name, file.data)
		if err != nil {
			continue
		}
		content = strings.TrimSpace(content)
		if content == "" {
			catalog = append(catalog, fmt.Sprintf("- %s (tidak ada teks yang bisa diekstrak)", file.name))
			continue
		}

		catalog = append(catalog, fmt.Sprintf("- %s", file.name))
		matches = append(matches, scoreChunks(file.name, content, terms)...)
	}

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].score == matches[j].score {
			return len(matches[i].text) > len(matches[j].text)
		}
		return matches[i].score > matches[j].score
	})

	var sb strings.Builder
	if len(catalog) > 0 && maxChars >= 2500 {
		sb.WriteString("DAFTAR FILE KNOWLEDGE TERSEDIA:\n")
		sb.WriteString(strings.Join(catalog, "\n"))
		sb.WriteString("\n\n")
	}

	sb.WriteString("POTONGAN KNOWLEDGE PALING RELEVAN:\n")
	written := sb.Len()
	count := 0
	for _, match := range matches {
		if match.score <= 0 && count > 0 {
			break
		}

		block := fmt.Sprintf("\n--- FILE: %s ---\n%s\n", match.fileName, match.text)
		if written+len(block) > maxChars {
			remaining := maxChars - written
			if remaining <= 200 {
				break
			}
			block = block[:remaining]
		}
		sb.WriteString(block)
		written += len(block)
		count++

		if written >= maxChars {
			break
		}
	}

	return strings.TrimSpace(sb.String())
}

func configuredMaxChars() int {
	raw := strings.TrimSpace(os.Getenv("KNOWLEDGE_MAX_CHARS"))
	if raw == "" {
		return defaultMaxChars
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return defaultMaxChars
	}
	return n
}

func sourceFiles(dir string) []knowledgeFile {
	return localKnowledgeFiles(dir)
}

func localKnowledgeFiles(dir string) []knowledgeFile {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	files := []knowledgeFile{}
	for _, entry := range entries {
		if entry.IsDir() || !isSupportedKnowledgeName(entry.Name()) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		files = append(files, knowledgeFile{name: entry.Name(), data: data})
	}
	return files
}

func isSupportedKnowledgeName(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".txt", ".md", ".pdf", ".docx":
		return true
	default:
		return false
	}
}

func readKnowledgeData(name string, data []byte) (string, error) {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".txt", ".md":
		return string(data), nil
	case ".pdf":
		return readPDFBytes(data)
	case ".docx":
		return readDOCXBytes(data)
	default:
		return "", nil
	}
}

func readPDFBytes(data []byte) (string, error) {
	reader, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", err
	}

	plainText, err := reader.GetPlainText()
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, plainText); err != nil {
		return "", err
	}

	return normalizeText(buf.String()), nil
}

func readDOCXBytes(data []byte) (string, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", err
	}

	for _, file := range reader.File {
		if file.Name != "word/document.xml" {
			continue
		}

		rc, err := file.Open()
		if err != nil {
			return "", err
		}
		defer rc.Close()

		return readWordDocumentXML(rc)
	}

	return "", nil
}

func readWordDocumentXML(r io.Reader) (string, error) {
	decoder := xml.NewDecoder(r)
	var sb strings.Builder

	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}

		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}

		switch start.Name.Local {
		case "t":
			var text string
			if err := decoder.DecodeElement(&text, &start); err != nil {
				return "", err
			}
			sb.WriteString(text)
		case "tab":
			sb.WriteString("\t")
		case "br", "p":
			sb.WriteString("\n")
		}
	}

	return normalizeText(sb.String()), nil
}

func normalizeText(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	blank := false

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			if !blank {
				out = append(out, "")
			}
			blank = true
			continue
		}
		out = append(out, line)
		blank = false
	}

	return strings.TrimSpace(strings.Join(out, "\n"))
}

type scoredChunk struct {
	fileName string
	text     string
	score    int
}

func scoreChunks(fileName, content string, terms []string) []scoredChunk {
	var chunks []string
	if len(content) <= 10000 {
		chunks = []string{content}
	} else {
		chunks = splitIntoChunks(content, 1000)
	}
	scored := make([]scoredChunk, 0, len(chunks))
	lowerFileName := strings.ToLower(fileName)

	for _, chunk := range chunks {
		lowerChunk := strings.ToLower(chunk)
		
		// Bersihkan karakter non-huruf/angka untuk dipecah menjadi kata-kata
		words := strings.FieldsFunc(lowerChunk, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r)
		})

		score := 0
		uniqueMatches := 0
		for _, term := range terms {
			termMatched := false
			
			// 1. Cek kecocokan di nama file untuk konteks dokumen global
			if strings.Contains(lowerFileName, term) {
				score += 5
				termMatched = true
			}

			// 2. Cocokkan persis (exact match) di teks chunk
			exactCount := strings.Count(lowerChunk, term)
			if exactCount > 0 {
				score += exactCount * 2 // Bobot frekuensi kemunculan
				termMatched = true
			} else {
				// 3. Cari kecocokan fuzzy jika tidak ada exact match di teks chunk
				for _, w := range words {
					if isFuzzyMatch(w, term) {
						score += 1
						termMatched = true
						break // Satu kecocokan per term di kata yang sama
					}
				}
			}
			if termMatched {
				uniqueMatches++
			}
		}

		// Tambahkan bobot besar berdasarkan jumlah kata unik yang berhasil dicocokkan.
		// Ini memastikan chunk yang mencocokkan "pembina", "ipnu", DAN "magetan" secara bersamaan
		// akan jauh mengungguli chunk yang hanya mengulang-ulang kata "ipnu" saja.
		score += uniqueMatches * 100

		scored = append(scored, scoredChunk{
			fileName: fileName,
			text:     chunk,
			score:    score,
		})
	}

	return scored
}

func isFuzzyMatch(word, term string) bool {
	if len(word) < 3 || len(term) < 3 {
		return word == term
	}
	if strings.Contains(word, term) || strings.Contains(term, word) {
		return true
	}

	maxDist := 1
	if len(term) >= 8 {
		maxDist = 2
	}

	// Selisih panjang tidak boleh melebihi jarak maksimum
	diff := len(word) - len(term)
	if diff < 0 {
		diff = -diff
	}
	if diff > maxDist {
		return false
	}

	return levenshteinDistance(word, term) <= maxDist
}

func levenshteinDistance(s, t string) int {
	d := make([][]int, len(s)+1)
	for i := range d {
		d[i] = make([]int, len(t)+1)
		d[i][0] = i
	}
	for j := range d[0] {
		d[0][j] = j
	}
	for i := 1; i <= len(s); i++ {
		for j := 1; j <= len(t); j++ {
			cost := 1
			if s[i-1] == t[j-1] {
				cost = 0
			}
			d[i][j] = minInt(d[i-1][j]+1, minInt(d[i][j-1]+1, d[i-1][j-1]+cost))
		}
	}
	return d[len(s)][len(t)]
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func splitIntoChunks(content string, maxChars int) []string {
	paragraphs := strings.Split(content, "\n")
	chunks := []string{}
	var current strings.Builder

	flush := func() {
		text := strings.TrimSpace(current.String())
		if text != "" {
			chunks = append(chunks, text)
		}
		current.Reset()
	}

	for _, paragraph := range paragraphs {
		paragraph = strings.TrimSpace(paragraph)
		if paragraph == "" {
			continue
		}

		if len(paragraph) > maxChars {
			flush()
			for len(paragraph) > maxChars {
				chunks = append(chunks, strings.TrimSpace(paragraph[:maxChars]))
				paragraph = paragraph[maxChars:]
			}
		}

		if current.Len()+len(paragraph)+1 > maxChars {
			flush()
		}
		current.WriteString(paragraph)
		current.WriteString("\n")
	}
	flush()

	return chunks
}

func queryTerms(query string) []string {
	stopwords := map[string]struct{}{
		"apa": {}, "aja": {}, "saja": {}, "yang": {}, "dan": {}, "atau": {}, "di": {}, "ke": {},
		"dari": {}, "ini": {}, "itu": {}, "kalau": {}, "kalo": {}, "anda": {}, "punya": {},
		"memiliki": {}, "data": {}, "selain": {}, "tentang": {}, "untuk": {},
	}

	seen := map[string]struct{}{}
	terms := []string{}
	for _, token := range strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if len(token) < 3 {
			continue
		}
		if _, skip := stopwords[token]; skip {
			continue
		}
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		terms = append(terms, token)
	}

	return terms
}
