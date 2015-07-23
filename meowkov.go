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
	version           = botName + " v0.1"
	redisHost         = "localhost:6379"
)

var (
	ownMention   = regexp.MustCompile(botName + "_*[:,]*\\s*")
	otherMention = regexp.MustCompile("^\\S+[:,]+\\s+")
	Corpus       redis.Conn
)

func printErr(err error) {
	fmt.Fprintf(os.Stderr, "\n[redis error]: %v\n", err.Error())
}

func main() {

	rdb, err := redis.Dial("tcp", redisHost)
	if err != nil {
		print(err)
		os.Exit(1)
	}
	Corpus = rdb

	rand.Seed(time.Now().Unix())

	con := irc.IRC(botName, botName)
	con.UseTLS = true
	con.Debug = true
	con.Version = version
	con.Connect("chat.freenode.net:7000")

	con.AddCallback("001", func(e *irc.Event) {
		con.Join(roomName)
	})

	con.AddCallback("JOIN", func(e *irc.Event) {
		con.Privmsg(roomName, "kek")
	})

	con.AddCallback("PRIVMSG", func(e *irc.Event) {
		response, incentive := processInput(e.Message())
		if len(response) > 0 && response != stop && incentive > rand.Float64() {
			channel := e.Arguments[0]
			con.Privmsg(channel, response)
		}
	})

	con.Loop()
}

func processInput(message string) (string, float64) {

	words, chattiness := normalizeInput(message)
	groups := generateChainGroups(words)

	if chainLength < len(words) && len(words) <= maxChainLength {
		addToCorpus(groups)
	}

	response := generateResponse(groups)

	return response, chattiness
}

func normalizeInput(message string) ([]string, float64) {
	chattiness := defaultChattiness
	message = strings.ToLower(message)

	if ownMention.MatchString(message) {
		message = ownMention.ReplaceAllString(message, "")
		chattiness = 1.0
	}
	if otherMention.MatchString(message) {
		message = otherMention.ReplaceAllString(message, "")
	}

	tokens := strings.Split(message, " ")
	words := make([]string, 0)

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
		fmt.Println("group #" + fmt.Sprint(i) + ": " + dump(group))
		cut := len(group) - 1
		key := strings.Join(group[:cut], separator)
		value := group[cut:][0]
		_, err := Corpus.Do("SADD", key, value)
		if err != nil {
			printErr(err)
			continue
		}

		chainValues, err := redis.Strings(Corpus.Do("SMEMBERS", key))
		if err != nil {
			printErr(err)
		}
		fmt.Println("Corpus[" + key + "]=" + dump(chainValues))
	}
}

func generateChainGroups(words []string) [][]string {
	length := len(words)
	groups := make([][]string, 0)

	for i, _ := range words {
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
	responses := make([]string, 0)

	for _, group := range groups {
		best := ""
		for range [chainsToTry]struct{}{} {
			response := randomResponse(group)
			if len(response) > len(best) {
				best = response
			}
		}
		if len(best) > 2 && best != groups[0][0] {
			responses = append(responses, best)
		}
	}

	responses = deduplicate(responses)
	fmt.Print("responses: " + dump(responses))

	count := len(responses)
	if count > 0 {
		return responses[rand.Intn(count)]
	} else {
		return stop
	}

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

func randomResponse(words []string) string {

	chainKey := strings.Join(words[:chainLength], separator)
	response := []string{words[0]}

	//fmt.Print("rootKey: " + chainKey)

	for range [maxChainLength]struct{}{} {
		word := randomWord(chainKey)
		if len(word) > 0 && word != stop {
			words = append(words[1:], word)
			response = append(response, words[0])
			chainKey = strings.Join(words, separator)
			//fmt.Print(" | " + chainKey)
		} else {
			break
		}
	}

	//fmt.Println("\tresponse: " + dump(response))

	return strings.Join(response, " ")
}

func randomWord(key string) string {
	value, err := redis.String(Corpus.Do("SRANDMEMBER", key))
	if err == nil || err == redis.ErrNil {
		return value
	} else {
		printErr(err)
	}
	return stop

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
