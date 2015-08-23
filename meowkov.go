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
	"log"
	"math/rand"
	"net"
	"os"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

var config struct {
	BotName     string
	RoomName    string
	IrcServer   string
	IrcPassword string
	UseTLS      bool
	Debug       bool

	RedisServer      string
	CorpusPerChannel bool

	ChainLength      int64
	MaxChainLength   int64
	ChainsToTry      int64
	MinResponsePool  int64
	MaxResponseTries int64

	DefaultChattiness float64
	SmileyChance      float64
	WordsPerMinute    int64

	Smileys     []string
	DontEndWith []string
	Blacklist   []string
}

const (
	stop          = "\x01"
	separator     = "\x02"
	always        = 1.0
	defaultConfig = "meowkov.conf"
)

var (
	pool    *redis.Pool
	version string

	ownMention   *regexp.Regexp
	otherMention *regexp.Regexp
	httpLink     *regexp.Regexp
	textCruft    *regexp.Regexp
)

func loadConfig(file string) (bool, bool) {
	var (
		confPath    = flag.String("c", file, "path to the config file")
		justImport  = flag.Bool("import", false, "If true, read messages from piped stdin instead of IRC")
		purgeCorpus = flag.Bool("purge", false, "If true, removes old corpus before importing anything")
		errorPrefix = "Error during loadConfig(): "
	)
	flag.Parse()

	if config.Debug {
		log.Println("Loading config file: " + *confPath)
	}

	jsonData, err := ioutil.ReadFile(*confPath)
	check(err, errorPrefix)
	err = json.Unmarshal(jsonData, &config)
	check(err, errorPrefix)

	if config.Debug {
		log.Printf("%#v\n", config)
	}

	// init Redis
	redisServer := getRedisServer()
	pool = &redis.Pool{
		MaxIdle:     3,
		MaxActive:   100,
		Wait:        true,
		IdleTimeout: 1 * time.Second,
		Dial: func() (redis.Conn, error) {
			c, err := redis.Dial("tcp", redisServer)
			if err != nil {
				return nil, err
			}
			return c, err
		},
		TestOnBorrow: func(c redis.Conn, t time.Time) error {
			_, err := c.Do("PING")
			return err
		},
	}

	// irc server validation
	_, _, err = net.SplitHostPort(config.IrcServer)
	check(err, errorPrefix)

	// other inits
	runtime.GOMAXPROCS(runtime.NumCPU())
	rand.Seed(time.Now().Unix())

	// detect when own nick is mentioned or when message is directed to other person
	ownMention = regexp.MustCompile("(?i)_*" + regexp.QuoteMeta(config.BotName) + "_*[:,]*\\s*")
	otherMention = regexp.MustCompile("(?i)^\\S+[:,]+\\s+")
	// detect HTTP(s) URLs
	httpLink = regexp.MustCompile("^http(s)?://[^/]")
	// remove single and double quotes, parentheses and ?!, leave semicolons and commas
	textCruft = regexp.MustCompile(`^[\"'\(\[]*([^\"'\?!\)\]]+)[\"'\?!\)\]]*$`)

	return *justImport, *purgeCorpus
}

func main() {
	justImport, mode := loadConfig(defaultConfig)
	defer pool.Close()

	if justImport {
		importLoop(mode)
	} else {
		ircLoop()
	}
}

func importLoop(newCorpus bool) {
	fi, err := os.Stdin.Stat()
	check(err, "importLoop is unable to get stdin: ")
	if fi.Mode()&os.ModeNamedPipe == 0 {
		log.Panicln("no input: please pipe some data in and try again")
	} else {
		config.Debug = false // improve load performance
		if newCorpus {
			log.Println("PURGE: removing old corpus")
			purgeCorpus()
		}
		log.Println("IMPORT: loading piped data into corpus at " + config.RedisServer)
		reader := bufio.NewReader(os.Stdin)

		var (
			wg  sync.WaitGroup
			sem = make(chan int, runtime.NumCPU()*1000)
		)
		i := 0
		for {
			sem <- 1
			line, err := reader.ReadString('\n')
			if err != nil {
				if err != io.EOF {
					panic(err)
				}
				break
			}
			i++
			wg.Add(1)
			go func(line string) {
				defer wg.Done()
				processInput(line, true)
				<-sem
			}(line)
		}
		wg.Wait()

		log.Println("IMPORT finished, processed " + fmt.Sprint(i) + " lines")
	}

}

func ircLoop() {
	con := irc.IRC(config.BotName, config.BotName)
	con.UseTLS = config.UseTLS
	con.Debug = config.Debug
	con.Version = "meowkov @ " + version + " (https://github.com/lidel/meowkov)"
	if config.IrcPassword != "" {
		con.Password = config.IrcPassword
	}

	con.Connect(config.IrcServer)

	con.AddCallback("001", func(e *irc.Event) {
		con.Join(config.RoomName)
	})

	con.AddCallback("JOIN", func(e *irc.Event) {
		con.Privmsg(config.RoomName, randomSmiley())
	})

	con.AddCallback("PRIVMSG", func(e *irc.Event) {
		channel := strings.Split(e.Raw, " ")[2]
		privateQuery := channel == con.GetNick()

		if privateQuery {
			channel = strings.Split(e.Raw[1:], "!")[0]
		}

		words, seeds := processInput(e.Message(), !privateQuery)
		chattiness := calculateChattiness(e.Message(), con.GetNick())

		if privateQuery || chattiness > rand.Float64() {
			response := generateResponse(words, seeds, int(config.MaxResponseTries))
			if chattiness == always {
				response = e.Nick + ": " + strings.TrimSpace(response)
			}
			typingDelay(response)
			con.Privmsg(channel, response)
		}
	})

	con.Loop()
}

func calculateChattiness(message string, currentBotNick string) float64 {
	chattiness := config.DefaultChattiness
	if strings.Contains(message, currentBotNick) || ownMention.MatchString(message) {
		chattiness = always
	}
	return chattiness
}

func getRedisServer() string {
	redisHost, redisPort, err := net.SplitHostPort(config.RedisServer)
	check(err, "getRedisServer() is unable to get value from config file: ")

	// support for dockerized redis
	env := "REDIS_PORT_" + redisPort + "_TCP_ADDR"
	host := os.Getenv(env)
	if host != "" {
		redisHost = host
		if config.Debug {
			log.Println("Using Dockerized Redis: " + env + "=" + fmt.Sprint(host))
		}
	}

	return redisHost + ":" + redisPort
}

func isEmpty(text string) bool {
	return len(text) == 0 || text == stop
}

func isChainEmpty(texts []string) bool {
	return len(texts) == 0 || (len(texts) == 1 && texts[0] == stop || texts[0] == "")
}

func typingDelay(text string) {
	// https://en.wikipedia.org/wiki/Words_per_minute
	typing := ((float64(len(text)) / 5) / float64(config.WordsPerMinute)) * 60
	if config.Debug {
		log.Println("Typing delay: " + fmt.Sprint(typing))
	}
	time.Sleep(time.Duration(typing) * time.Second)
}

func processInput(message string, learning bool) (words []string, seed [][]string) {
	words = parseInput(message)
	seed = createSeeds(words)
	if learning && int(config.ChainLength) < len(words) {
		addToCorpus(seed)
	}
	return
}

func parseInput(message string) []string {
	if otherMention.MatchString(message) {
		message = otherMention.ReplaceAllString(message, "")
	}

	var (
		tokens = strings.Split(message, " ")
		words  []string
	)

	for _, token := range tokens {
		if word := normalizeWord(token); len(word) > 0 {
			words = append(words, word)
		}
	}

	return append(words, stop)
}

// normalizeWord removes various cruft from parsed text.
// The goal is to make corpus more uniform (no duplicate clusters for multiple versions of the same word)
func normalizeWord(word string) string {
	word = strings.TrimSpace(word)
	if !httpLink.MatchString(word) { // don't change URLs
		word = strings.ToLower(word)
		word = textCruft.ReplaceAllString(word, "$1")
	}
	return word
}

func addToCorpus(seeds [][]string) {
	corpus := pool.Get()
	defer corpus.Close()
	for i, seed := range seeds {

		cut := len(seed) - 1
		key := corpusKey(strings.Join(seed[:cut], separator))
		value := seed[cut:][0]

		_, err := corpus.Do("SADD", key, value)
		if err != nil {
			redisErr(err)
			return
		}

		if config.Debug {
			log.Println("seed  #" + fmt.Sprint(i) + ":\t" + dump(seed))
			chainValues, err := redis.Strings(corpus.Do("SMEMBERS", key))
			if err != nil {
				redisErr(err)
				return
			}
			log.Println("corpus #" + fmt.Sprint(i) + ":\t" + dump(chainValues))
		}
	}
}

func corpusKey(key string) string {
	if config.CorpusPerChannel {
		key = config.RoomName + separator + key
	}
	return key
}

// [1 2 3 4 \x01] → [[1 2 3][2 3 4][3 4 \x01]]
func createSeeds(words []string) [][]string {
	var (
		seeds  [][]string
		length = len(words)
		min    = int(config.ChainLength)
	)

	for i := range words {
		end := i + min + 1

		if end > length {
			end = length
		}
		if end-i <= min {
			break
		}

		seeds = append(seeds, words[i:end])
	}

	return seeds
}

func generateResponse(input []string, seeds [][]string, triesLeft int) string {
	var (
		responses []string
		response  string
	)

	if config.Debug {
		log.Println("Generating response for input: " + dump(input))
	}

	var wg sync.WaitGroup
	var mtx sync.Mutex
	for _, seed := range seeds {
		wg.Add(1)
		go func(seed []string) {
			defer wg.Done()
			for i := 0; i < int(config.ChainsToTry); i++ {
				if response := randomBranch(seed); notPresent(response, seed) {
					mtx.Lock()
					responses = append(responses, response)
					mtx.Unlock()
				}
				runtime.Gosched()
			}
		}(seed)
	}
	wg.Wait()

	responses = normalizeResponseChains(responses)

	if config.Debug {
		log.Println("Found " + fmt.Sprint(len(responses)) + " potential responses:\n" + dump(responses))
	}

	count := len(responses)
	if count >= int(config.MinResponsePool) {
		response = responses[rand.Intn(count)]
		response = response + " " + randomSmiley()
	} else if triesLeft > 0 {
		triesLeft--
		try := int(config.MaxResponseTries) - triesLeft
		power := try * try * try * try
		if config.Debug {
			log.Println("Pool of responses is too small, trying again with artificialSeed^" + fmt.Sprint(power))
		}
		seeds = artificialSeed(input, power)
		response = generateResponse(input, seeds, triesLeft)
	} else {
		response = randomSmiley()
	}

	return response
}

func notPresent(item string, items []string) bool {
	for _, oldItem := range items {
		if item == oldItem {
			return false
		}

	}
	return true

}

func randomBranch(words []string) string {
	chain := words[:config.ChainLength]

	chainKey := strings.Join(chain, separator)
	response := []string{chain[0]}

	corpus := pool.Get()
	defer corpus.Close()

	for i := 0; i < int(config.MaxChainLength); i++ {
		word := randomWord(chainKey, corpus)
		if !isEmpty(word) {
			chain = append(chain[1:], word)
			response = append(response, chain[0])
			chainKey = strings.Join(chain, separator)
		} else {
			break
		}
	}

	response = removeBlacklistedWords(response)

	return strings.Join(response, " ")
}

func randomWord(key string, corpus redis.Conn) string {
	value, err := redis.String(corpus.Do("SRANDMEMBER", corpusKey(key)))
	if err == nil || err == redis.ErrNil {
		return value
	}
	redisErr(err)
	return stop
}

func randomChain(corpus redis.Conn) []string {
	value, err := redis.String(corpus.Do("RANDOMKEY"))
	if err != nil && err != redis.ErrNil {
		redisErr(err)
	}
	return strings.Split(value, separator)
}

func purgeCorpus() {
	corpus := pool.Get()
	defer corpus.Close()
	_, err := corpus.Do("FLUSHDB")
	if err != nil {
		redisErr(err)
		panic(err)
	}
}

func artificialSeed(input []string, power int) [][]string {
	var result [][]string

	if isChainEmpty(input) {
		corpus := pool.Get()
		input = randomChain(corpus)[:1]
		corpus.Close()
	}

	var wg sync.WaitGroup
	var mtx sync.Mutex
	for _, word := range input {
		if word == stop {
			break
		}
		for i := 0; i < power; i++ {
			wg.Add(1)
			go func(word string, i int) {
				corpus := pool.Get()
				defer corpus.Close()
				defer wg.Done()
				for _, mutation := range createSeeds(mutateChain(word, randomChain(corpus))) {
					mtx.Lock()
					result = append(result, mutation)
					mtx.Unlock()
					runtime.Gosched()
				}
			}(word, i)
		}
	}
	wg.Wait()

	/*if config.Debug {
		log.Println("artificialSeed(", dump(input)+", "+fmt.Sprint(power)+")="+fmt.Sprint(result))
	}*/

	return result
}

// (A, [1 2]) → [A 1 A 2 A]
func mutateChain(word string, chain []string) []string {
	mutation := []string{word}
	for _, item := range chain {
		mutation = append(mutation, []string{item, word}...)
	}
	return mutation
}

func randomSmiley() string {
	return config.Smileys[rand.Intn(len(config.Smileys))]
}

func removeBlacklistedWords(words []string) []string {
	data := make([]string, len(words))
	end := 0

Blacklist:
	for _, word := range words {
		for _, bad := range config.Blacklist {
			if word == bad {
				continue Blacklist
			}
		}
		data[end] = word
		end++
	}
	words = data[:end]

DontEndWith:
	for {
		length := len(words)
		for remove := range config.DontEndWith {
			if length > 0 && words[length-1] == config.DontEndWith[remove] {
				words = words[:length-1]
				continue DontEndWith // ending changed, restart loop
			}

		}
		break
	}

	return words
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
	log.Println("Discarded responses shorter than the median of " + fmt.Sprint(threshold) + " characters")

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

func check(e error, message string) {
	if e != nil {
		if message != "" {
			log.Println(message)
		}
		log.Panicln(e)
	}
}
