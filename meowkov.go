package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/garyburd/redigo/redis"
	"github.com/thoj/go-ircevent"
	"io"
	"io/ioutil"
	"math/rand"
	"net"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

// MeowkovConfig defines key names of the config file in JSON format
type MeowkovConfig struct {
	BotName   string
	RoomName  string
	IrcServer string
	UseTLS    bool
	Debug     bool

	RedisServer      string
	CorpusPerChannel bool

	ChainLength    int64
	MaxChainLength int64
	ChainsToTry    int64

	DefaultChattiness float64
	SmileyChance      float64
	WordsPerMinute    int64

	Smileys []string
}

const (
	stop      = "\x01"
	separator = "\x02"
	always    = 1.0
)

var (
	corpus  redis.Conn
	config  MeowkovConfig
	version string

	ownMention   *regexp.Regexp
	otherMention *regexp.Regexp
)

func loadConfig() {
	confPath := flag.String("c", "meowkov.conf", "path to the config file")
	flag.Parse()

	fmt.Println("Loading config file: " + *confPath)

	jsonData, err := ioutil.ReadFile(*confPath)
	check(err)
	err = json.Unmarshal(jsonData, &config)
	check(err)

	fmt.Printf("%#v\n", config)

	// init Redis
	rdb, err := redis.Dial("tcp", redisAddr())
	if err != nil {
		redisErr(err)
		os.Exit(1)
	}
	corpus = rdb

	// irc server validation
	_, _, err = net.SplitHostPort(config.IrcServer)
	check(err)

	// other inits
	rand.Seed(time.Now().Unix())
	ownMention = regexp.MustCompile(config.BotName + "_*[:,]*\\s*")
	otherMention = regexp.MustCompile("^\\S+[:,]+\\s+")
}

func main() {
	importMode := flag.Bool("import", false, "If true, read messages from piped stdin instead of IRC")
	loadConfig()
	if *importMode {
		importLoop()
	} else {
		ircLoop()
	}
}

func importLoop() {
	fi, err := os.Stdin.Stat()
	check(err)
	if fi.Mode()&os.ModeNamedPipe == 0 {
		fmt.Fprintln(os.Stderr, "no input: please pipe some data in and try again")
		os.Exit(1)
	} else {
		config.Debug = false // improve load performance
		fmt.Println("IMPORT: loading piped data into corpus at " + config.RedisServer)
		reader := bufio.NewReader(os.Stdin)
		i := 0
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				if err != io.EOF {
					panic(err)
				}
				fmt.Println("IMPORT finished, processed " + fmt.Sprint(i) + " lines")
				break
			}
			processInput(line)
			i++
		}
	}

}

func ircLoop() {
	con := irc.IRC(config.BotName, config.BotName)
	con.UseTLS = config.UseTLS
	con.Debug = config.Debug
	con.Version = "meowkov @ " + version + " (https://github.com/lidel/meowkov)"

	con.Connect(config.IrcServer)

	con.AddCallback("001", func(e *irc.Event) {
		con.Join(config.RoomName)
	})

	con.AddCallback("JOIN", func(e *irc.Event) {
		con.Privmsg(config.RoomName, randomSmiley())
	})

	con.AddCallback("PRIVMSG", func(e *irc.Event) {
		seed, chattiness := processInput(e.Message())

		if chattiness > rand.Float64() {
			response := generateResponse(seed)
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
	redisHost, redisPort, err := net.SplitHostPort(config.RedisServer)
	check(err)

	// support for dockerized redis
	env := "REDIS_PORT_" + redisPort + "_TCP_ADDR"
	host := os.Getenv(env)
	if host != "" {
		redisHost = host
		if config.Debug {
			fmt.Println(env + "=" + fmt.Sprint(host))
		}
	}

	return redisHost + ":" + redisPort
}

func isEmpty(text string) bool {
	return len(text) == 0 || text == stop
}

func typingDelay(text string) {
	// https://en.wikipedia.org/wiki/Words_per_minute
	typing := ((float64(len(text)) / 5) / float64(config.WordsPerMinute)) * 60
	if config.Debug {
		fmt.Println("Typing delay: " + fmt.Sprint(typing))
	}
	time.Sleep(time.Duration(typing) * time.Second)
}

func processInput(message string) ([][]string, float64) {
	words, chattiness := parseInput(message)
	groups := generateChainGroups(words)

	if int(config.ChainLength) < len(words) {
		addToCorpus(groups)
	}

	return groups, chattiness
}

func parseInput(message string) ([]string, float64) {
	chattiness := config.DefaultChattiness
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

		if config.Debug {
			fmt.Println("group  #" + fmt.Sprint(i) + ":\t" + dump(group))
		}

		cut := len(group) - 1
		key := corpusKey(strings.Join(group[:cut], separator))
		value := group[cut:][0]

		_, err := corpus.Do("SADD", key, value)
		if err != nil {
			redisErr(err)
			continue
		}

		if config.Debug {
			chainValues, err := redis.Strings(corpus.Do("SMEMBERS", key))
			if err != nil {
				redisErr(err)
				continue
			}
			fmt.Println("corpus #" + fmt.Sprint(i) + ":\t" + dump(chainValues))
		}
	}
}

func corpusKey(key string) string {
	if config.CorpusPerChannel {
		key = config.RoomName + separator + key
	}
	return key
}

func generateChainGroups(words []string) [][]string {
	var (
		groups [][]string
		length = len(words)
		max    = int(config.ChainLength)
	)

	for i := range words {
		end := i + max + 1

		if end > length {
			end = length
		}
		if end-i <= max {
			break
		}

		groups = append(groups, words[i:end])
	}

	return groups
}

func generateResponse(groups [][]string) string {
	var (
		responses []string
		response  string
	)

	if len(groups) < int(config.ChainLength) {
		// when seed is smaller than chain size, markov is not effective
		// so random one is better than nothing
		groups = randomSeed(4)
	}

	for _, group := range groups {
		for i := 0; i < int(config.ChainsToTry); i++ {
			responses = append(responses, randomChain(group))
		}
	}

	responses = normalizeResponseChains(responses)

	if config.Debug {
		fmt.Println("responses:\t" + dump(responses))
	}

	count := len(responses)
	if count > 1 {
		response = responses[rand.Intn(count)]
	} else {
		response = stop
	}

	if isEmpty(response) {
		response = randomSmiley()
	} else if config.SmileyChance > rand.Float64() {
		response = response + " " + randomSmiley()
	}

	return response
}

func randomChain(words []string) string {
	chain := words[:config.ChainLength]

	chainKey := strings.Join(chain, separator)
	response := []string{chain[0]}

	for i := 0; i < int(config.MaxChainLength); i++ {
		word := randomWord(chainKey)
		if !isEmpty(word) {
			chain = append(chain[1:], word)
			response = append(response, chain[0])
			chainKey = strings.Join(chain, separator)
		} else {
			break
		}
	}
	if config.Debug {
		fmt.Println("\trandomChain:\t" + dump(response))
	}

	return strings.Join(response, " ")
}

func randomWord(key string) string {
	value, err := redis.String(corpus.Do("SRANDMEMBER", corpusKey(key)))
	if err == nil || err == redis.ErrNil {
		return value
	}
	redisErr(err)
	return stop
}

func randomSeed(times int) [][]string {
	var seed [][]string
	for i := 0; i < times; i++ {
		value, err := redis.String(corpus.Do("RANDOMKEY"))
		if err != nil && err != redis.ErrNil {
			redisErr(err)
			continue
		}
		chainKey := strings.Split(value, separator)
		seed = append(seed, chainKey)
	}

	return seed
}

func randomSmiley() string {
	return config.Smileys[rand.Intn(len(config.Smileys))]
}

func normalizeResponseChains(texts []string) []string {
	if len(texts) == 0 {
		return texts
	}

	// deduplicate
	l := map[int]struct{}{}
	m := map[string]struct{}{}
	for _, text := range texts {
		if _, ok := m[text]; !ok {
			m[text] = struct{}{}
			l[len(text)] = struct{}{}
		}
	}
	list := make([]string, len(m))
	i := 0
	for v := range m {
		list[i] = v
		i++
	}

	// drop bottom half (below median)
	var result []string
	lengths := make([]int, 0, len(l))
	for k := range l {
		lengths = append(lengths, k)
	}
	sort.Ints(lengths)
	threshold := median(lengths)
	for i := range list {
		text := list[i]
		if len(text) >= threshold {
			result = append(result, text)
		}
	}
	if config.Debug {
		fmt.Println("Discarded responses shorter than median of " + fmt.Sprint(threshold) + " characters")
	}

	return result
}

func median(numbers []int) int {
	length := len(numbers)
	middle := length / 2

	result := numbers[middle]
	if middle > 0 && length%2 == 0 {
		result = (result + numbers[middle-1]) / 2
	}
	return result
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

func redisErr(err error) {
	fmt.Fprintf(os.Stderr, "\n[redis error]: %v\n", err.Error())
}

func check(e error) {
	if e != nil {
		panic(e)
	}
}
