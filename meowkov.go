package main

import (
	"bytes"
	"fmt"
	"github.com/garyburd/redigo/redis"
	"github.com/thoj/go-ircevent"
	"math/rand"
	"os"
	"regexp"
	"strings"
	"time"
)

const (
	botName           = "meowkov"
	roomName          = "#meowkov"
	chainLength       = 2
	maxChainLength    = 30
	chainsToTry       = 30
	defaultChattiness = 0.01
	stop              = "\x01"
	separator         = "\x02"
	always            = 1.0
	ircHost           = "chat.freenode.net:7000"
	corpusPerChannel  = false
	smileyChance      = 0.5
	debug             = true
	wordsPerMinute    = 100
)

var (
	corpus  redis.Conn
	version string

	ownMention   = regexp.MustCompile(botName + "_*[:,]*\\s*")
	otherMention = regexp.MustCompile("^\\S+[:,]+\\s+")
	smileys      = []string{"8<", ":-<", ":'-<", ":(", ":<", ":'<", ":--<", ":[", ":/", ":S", "\\:</", "xD", "D:", ":|", "kek", "( ͡° ͜ʖ ͡°)"}

	redisHost = "localhost"
	redisPort = "6379"
)

func main() {
	rdb, err := redis.Dial("tcp", redisAddr())
	if err != nil {
		printErr(err)
		os.Exit(1)
	}
	corpus = rdb

	rand.Seed(time.Now().Unix())

	con := irc.IRC(botName, botName)
	con.UseTLS = true
	con.Debug = debug
	con.Version = botName + "@" + version
	con.Connect(ircHost)

	con.AddCallback("001", func(e *irc.Event) {
		con.Join(roomName)
	})

	con.AddCallback("JOIN", func(e *irc.Event) {
		con.Privmsg(roomName, randomSmiley())
	})

	con.AddCallback("PRIVMSG", func(e *irc.Event) {
		response, chattiness := processInput(e.Message())

		if !isEmpty(response) && chattiness > rand.Float64() {
			if chattiness == always {
				response = e.Nick + ": " + strings.TrimSpace(response)
			}
			typingDelay(response)
			channel := e.Arguments[0]
			con.Privmsg(channel, response)
		}
	})

	con.Loop()
}

func redisAddr() string {
	// support for dockerized redis
	env := "REDIS_PORT_" + redisPort + "_TCP_ADDR"
	host := os.Getenv(env)
	if debug {
		fmt.Println(env + "=" + fmt.Sprint(host))
	}
	if host != "" {
		redisHost = host
	}
	return redisHost + ":" + redisPort
}

func isEmpty(text string) bool {
	return len(text) == 0 || text == stop
}

func typingDelay(text string) {
	// https://en.wikipedia.org/wiki/Words_per_minute
	typing := ((float64(len(text)) / 5) / wordsPerMinute) * 60
	if debug {
		fmt.Println("typing delay: " + fmt.Sprint(typing))
	}
	time.Sleep(time.Duration(typing) * time.Second)
}

func processInput(message string) (string, float64) {
	words, chattiness := parseInput(message)
	groups := generateChainGroups(words)

	if chainLength < len(words) && len(words) <= maxChainLength {
		addToCorpus(groups)
	}

	response := generateResponse(groups)

	if isEmpty(response) {
		response = randomSmiley()
	} else if smileyChance > rand.Float64() {
		response = response + " " + randomSmiley()
	}

	return response, chattiness
}

func parseInput(message string) ([]string, float64) {
	chattiness := defaultChattiness
	message = strings.ToLower(message)

	if ownMention.MatchString(message) {
		message = ownMention.ReplaceAllString(message, "")
		chattiness = always
	}
	if otherMention.MatchString(message) {
		message = otherMention.ReplaceAllString(message, "")
	}

	var (
		tokens = strings.Split(message, " ")
		words  []string
	)

	for _, token := range tokens {
		token = strings.TrimSpace(token)
		if len(token) > 0 {
			words = append(words, token)
		}
	}

	return append(words, stop), chattiness
}

func addToCorpus(groups [][]string) {
	for i, group := range groups {

		if debug {
			fmt.Println("group  #" + fmt.Sprint(i) + ":\t" + dump(group))
		}

		cut := len(group) - 1
		key := corpusKey(strings.Join(group[:cut], separator))
		value := group[cut:][0]

		_, err := corpus.Do("SADD", key, value)
		if err != nil {
			printErr(err)
			continue
		}

		chainValues, err := redis.Strings(corpus.Do("SMEMBERS", key))
		if err != nil {
			printErr(err)
		}

		if debug {
			fmt.Println("corpus #" + fmt.Sprint(i) + ":\t" + dump(chainValues))
		}
	}
}

func corpusKey(key string) string {
	if corpusPerChannel {
		key = roomName + key
	}
	return key
}

func generateChainGroups(words []string) [][]string {
	var (
		length = len(words)
		groups [][]string
	)

	for i := range words {
		end := i + chainLength + 1

		if end > length {
			end = length
		}
		if end-i <= chainLength {
			break
		}

		groups = append(groups, words[i:end])
	}

	return groups
}

func generateResponse(groups [][]string) string {
	var responses []string

	for _, group := range groups {
		best := ""

		for range [chainsToTry]struct{}{} {
			response := randomChain(group)
			if len(response) > len(best) {
				best = response
			}
		}

		if len(best) > 2 && best != groups[0][0] {
			responses = append(responses, best)
		}
	}

	responses = deduplicate(responses)

	if debug {
		fmt.Println("responses:\t" + dump(responses))
	}

	count := len(responses)
	if count > 0 {
		return responses[rand.Intn(count)]
	}
	return stop

}

func randomChain(words []string) string {
	chainKey := strings.Join(words[:chainLength], separator)
	response := []string{words[0]}

	for range [maxChainLength]struct{}{} {
		word := randomWord(chainKey)
		if len(word) > 0 && word != stop {
			words = append(words[1:], word)
			response = append(response, words[0])
			chainKey = strings.Join(words, separator)
		} else {
			break
		}
	}

	return strings.Join(response, " ")
}

func randomWord(key string) string {
	value, err := redis.String(corpus.Do("SRANDMEMBER", corpusKey(key)))
	if err == nil || err == redis.ErrNil {
		return value
	}
	printErr(err)
	return stop
}

func randomSmiley() string {
	return smileys[rand.Intn(len(smileys))]
}

func deduplicate(col []string) []string {
	m := map[string]struct{}{}
	for _, v := range col {
		if _, ok := m[v]; !ok {
			m[v] = struct{}{}
		}
	}
	list := make([]string, len(m))

	i := 0
	for v := range m {
		list[i] = v
		i++
	}
	return list
}

func dump(texts []string) string {
	var buffer bytes.Buffer

	buffer.WriteString("[")
	for i, text := range texts {
		if i > 0 {
			buffer.WriteString(", ")
		}
		buffer.WriteString("\"" + text + "\"")
	}
	buffer.WriteString("]")
	return buffer.String()
}

func printErr(err error) {
	fmt.Fprintf(os.Stderr, "\n[redis error]: %v\n", err.Error())
}
