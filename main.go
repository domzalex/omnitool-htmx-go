package main

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
)

type Quiz struct {
	Title          string           `form:"title"`
	Questions      map[int]Question `form:"-"`
	Answers        map[int]Answer   `form:"-"`
	CorrectAnswers []int            `form:"-"`
}

type Question struct {
	Title         string   `form:"title"`
	Answers       []string `form:"-"`
	CorrectAnswer int      `form:"-"`
}

type Answer struct {
	Title   string `form:"title"`
	Correct bool   `form:"-"`
}

type TempQuestions struct {
	qs [][]int
}

var (
	tmplQuestion  = template.Must(template.ParseFiles("templates/question.html"))
	tmplAnswer    = template.Must(template.ParseFiles("templates/answer.html"))
	quizTmpl      = template.Must(template.ParseFiles("templates/quiz_page.html"))
	questionID    int
	mu            sync.Mutex
	tempQuestions = TempQuestions{}
)

func extractNumbers(s string) (int, int, int) {
	seg := strings.Split(s, "-")
	fmt.Println(seg)
	var qnum int
	var anum int
	if len(seg) > 1 {
		qnum, _ = strconv.Atoi(seg[1])
		anum = 0
		if seg[2] != "ca" && seg[2] != "title" {
			anum, _ = strconv.Atoi(seg[3])
		}
	}
	return qnum, anum, len(seg)
}

func customSort(arr []string) {
	sort.Slice(arr, func(i, j int) bool {
		qnum1, anum1, segLen1 := extractNumbers(arr[i])
		qnum2, anum2, segLen2 := extractNumbers(arr[j])
		if qnum1 != qnum2 {
			return qnum1 < qnum2
		}
		if segLen1 != segLen2 {
			return segLen1 < segLen2
		}
		return anum1 < anum2
	})
}

func renderTemplate(c *gin.Context, tmpl *template.Template, data interface{}) {
	c.Writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.Execute(c.Writer, data); err != nil {
		c.String(http.StatusInternalServerError, err.Error())
	}
}

func addQuestionHandler(c *gin.Context) {
	mu.Lock()
	questionID++
	qID := questionID
	mu.Unlock()
	tempQuestions.qs = append(tempQuestions.qs, []int{})
	renderTemplate(c, tmplQuestion, qID)
}

func addAnswerHandler(c *gin.Context) {
	qIDStr := c.Query("question_id")
	qID, err := strconv.Atoi(qIDStr)
	if err != nil {
		c.String(http.StatusBadRequest, "Invalid question ID")
		return
	}
	al := len(tempQuestions.qs[qID-1]) + 1
	tempQuestions.qs[qID-1] = append(tempQuestions.qs[qID-1], al)
	data := struct {
		QuestionID int
		AnswerID   int
	}{
		QuestionID: qID,
		AnswerID:   al,
	}
	renderTemplate(c, tmplAnswer, data)
}

func renderForm(c *gin.Context) {
	questionID = 0
	tempQuestions = TempQuestions{}
	c.HTML(http.StatusOK, "index.html", nil)
}

func submitQuiz(c *gin.Context) {
	var quiz Quiz
	if err := c.ShouldBind(&quiz); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	quiz.Questions = make(map[int]Question)
	formData := c.Request.PostForm

	var keys []string
	for key := range formData {
		keys = append(keys, key)
	}
	// sort.Strings(keys)

	customSort(keys)

	fmt.Println("KEYS: ", keys)

	for _, key := range keys {
		values := c.Request.Form[key]
		if strings.HasPrefix(key, "question-") {
			parts := strings.Split(key, "-")
			if len(parts) < 3 {
				continue
			}

			questionID, err := strconv.Atoi(parts[1])
			if err != nil {
				continue
			}

			if _, exists := quiz.Questions[questionID]; !exists {
				quiz.Questions[questionID] = Question{
					Answers: make([]string, 0),
				}
			}

			question := quiz.Questions[questionID]

			switch parts[2] {
			case "title":
				question.Title = values[0]
			case "answer":
				if len(parts) == 4 {
					question.Answers = append(question.Answers, values[0])
				}
			default:
				if len(parts) == 3 {
					parts := strings.Split(values[0], "-")
					correctAnswerID, err := strconv.Atoi(parts[len(parts)-1])
					if err == nil {
						// fmt.Println("ID: ", correctAnswerID)
						question.CorrectAnswer = correctAnswerID
						quiz.CorrectAnswers = append(quiz.CorrectAnswers, correctAnswerID)
					}
				}
			}

			quiz.Questions[questionID] = question
		}
	}

	// Serialize to JSON
	jsonData, err := json.Marshal(quiz)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Compress using gzip
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	if _, err := gz.Write(jsonData); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := gz.Close(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Encode using base64
	encodedData := base64.URLEncoding.EncodeToString(b.Bytes())

	// Redirect or return the encoded data
	c.Header("HX-Redirect", "/quiz?data="+encodedData)
	c.Status(http.StatusOK)
}

func loadQuiz(c *gin.Context) {
	questionID = 0
	encodedData := c.Query("data")
	if encodedData == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No data provided"})
		return
	}

	// Decode from base64
	compressedData, err := base64.URLEncoding.DecodeString(encodedData)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Decompress using gzip
	r, err := gzip.NewReader(bytes.NewReader(compressedData))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	decompressedData, err := io.ReadAll(r)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := r.Close(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Deserialize to Quiz struct
	var quiz Quiz
	if err := json.Unmarshal(decompressedData, &quiz); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	renderTemplate(c, quizTmpl, gin.H{
		"quiz": quiz,
		"data": encodedData,
	})
}

func checkQuiz(c *gin.Context) {
	var selectedAnswers []int
	var answersCorrect int
	c.Request.ParseForm()
	formData := c.Request.Form
	data := c.QueryArray("data")[0]
	fmt.Println("DATA: ", data)
	dataFmt := strings.Split(data[1:len(data)-1], " ")

	var keys []string
	for key := range formData {
		if strings.HasPrefix(key, "question-") {
			keys = append(keys, key)
		}
	}

	sort.Slice(keys, func(a, b int) bool {
		anum, _ := strconv.Atoi(strings.TrimPrefix(keys[a], "question-"))
		bnum, _ := strconv.Atoi(strings.TrimPrefix(keys[b], "question-"))
		return anum < bnum
	})

	for _, key := range keys {
		values := formData[key]
		if len(values) > 0 {
			num, _ := strconv.Atoi(values[0])
			selectedAnswers = append(selectedAnswers, num+1)
		}
	}

	for i := 0; i < len(selectedAnswers); i++ {
		ans, _ := strconv.Atoi(dataFmt[i])
		if selectedAnswers[i] == ans {
			answersCorrect++
		}
	}
	fmt.Println(math.Round((float64(answersCorrect) / float64(len(selectedAnswers))) * 100))

	score := strconv.FormatFloat((math.Round((float64(answersCorrect) / float64(len(selectedAnswers))) * 100)), 'f', -1, 64)

	c.String(http.StatusOK, "Score: "+score+"%")
}

func main() {
	router := gin.New()
	router.LoadHTMLGlob("templates/*")
	router.GET("/", renderForm)
	router.POST("/send", submitQuiz)
	router.GET("/quiz", loadQuiz)
	router.POST("/add-question", addQuestionHandler)
	router.GET("/add-answer", addAnswerHandler)
	router.GET("/check-quiz", checkQuiz)
	router.Run(":3000")
}
