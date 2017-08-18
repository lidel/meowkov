package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"math/rand"
	"net"
	"os"
	"os/signal"
	"regexp"

	log "github.com/Sirupsen/logrus"
	"github.com/fiam/gounidecode/unidecode"
	"github.com/garyburd/redigo/redis"
	"github.com/thoj/go-ircevent"

	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

var config struct {
	BotName     string
	Channels    []string
	IrcServer   string
	IrcPassword string
	UseTLS      bool
	Debug       bool

	RedisServer string

	ChainLength      int64
	MaxChainLength   int64
	ChainsToTry      int64
	MinResponsePool  int64
	MaxResponseTries int64

	DefaultChattiness       float64
	MinTimeBetweenReactions int64
	SmileyChance            float64
	WordsPerMinute          int64

	Smileys             []string
	PredefinedResponses map[string]string
	DontEndWith         []string
	Blacklist           []string

	RoomName string `json:",omitempty"` // deprecated
}

const (
	stop          = "\x01"
	separator     = "\x02"
	always        = 1.0
	defaultConfig = "meowkov.conf"
)

var (
	pool         *redis.Pool
	lastReaction int64
	version      string

	ownMention    *regexp.Regexp
	otherMention  *regexp.Regexp
	httpLink      *regexp.Regexp
	textCruft     *regexp.Regexp
	emoticonCruft *regexp.Regexp
)

type uniqueTexts map[string]struct{}

func loadConfig(file string) (bool, bool) {
	var (
		confPath    = flag.String("c", file, "path to the config file")
		justImport  = flag.Bool("import", false, "If true, read messages from piped stdin instead of IRC")
		purgeCorpus = flag.Bool("purge", false, "If true, removes old corpus before importing anything")
		errorPrefix = "Error during loadConfig(): "
	)
	flag.Parse()

	log.Info("Loading config file: " + *confPath)

	jsonData, confError := ioutil.ReadFile(*confPath)
	check(confError, errorPrefix)
	confError = json.Unmarshal(jsonData, &config)
	check(confError, errorPrefix)

	if config.Debug {
		log.SetLevel(log.DebugLevel)
		//log.Debugf("%#v\n", config)
		c := reflect.ValueOf(&config).Elem()
		t := c.Type()
		secret := regexp.MustCompile("(?i)password")
		for i := 0; i < c.NumField(); i++ {
			f := c.Field(i)
			value := f.Interface()
			name := t.Field(i).Name
			if secret.MatchString(name) {
				value = "(secret)"
			}
			log.Debugf("\t%s = %v", t.Field(i).Name, value)
		}
	}

	// init Redis
	redisServer := getRedisServer()
	log.Println("Connecting to Redis at " + redisServer)
	pool = &redis.Pool{
		MaxIdle:     3,
		MaxActive:   10,
		Wait:        true,
		IdleTimeout: 240 * time.Second,
		Dial: func() (redis.Conn, error) {
			timeout := 500 * time.Millisecond
			c, err := redis.Dial("tcp", redisServer,
				redis.DialConnectTimeout(timeout),
				redis.DialReadTimeout(timeout),
				redis.DialWriteTimeout(timeout))
			if err != nil {
				return nil, err
			}
			return c, err
		},
		TestOnBorrow: func(c redis.Conn, t time.Time) error {
			// ping connections that were idle more than a minute
			if time.Since(t) < time.Minute {
				return nil
			}
			_, err := c.Do("PING")
			return err
		},
	}

	// irc server validation
	_, _, hostError := net.SplitHostPort(config.IrcServer)
	check(hostError, errorPrefix)

	// support legacy configs
	if len(config.Channels) == 0 && config.RoomName != "" {
		log.Fatalln("WARNING >>>> 'RoomName' is deprecated and will be removed in future. Use the 'Channels' list instead. Please update your config file.")
		config.Channels = []string{config.RoomName}
	}

	// other inits
	runtime.GOMAXPROCS(runtime.NumCPU())
	rand.Seed(time.Now().Unix())
	lastReaction = time.Now().UnixNano()

	// detect when own nick is mentioned or when message is directed to other person
	ownMention = regexp.MustCompile("(?i)_*" + regexp.QuoteMeta(config.BotName) + "_*[:,]*\\s*")
	otherMention = regexp.MustCompile(`(?i)^\S+[:,]+\s+`)
	// detect HTTP(s) URLs
	httpLink = regexp.MustCompile("^http(s)?://[^/]")
	// remove single and double quotes, parentheses and ?!, leave semicolons and commas
	textCruft = regexp.MustCompile(`^[„“\"'\(\[]*([^\"'\?!\)\]„“”]+)[”“\"'\?!\)\]]*$`)
	// remove emoticons
	emoticonCruft = regexp.MustCompile(`^([;:8]["'-^]*[\[\(\]\)<DPdoOcCp]+)$`)

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

	log.Println("Connecting to IRC at " + config.IrcServer)
	con.Connect(config.IrcServer)

	con.AddCallback("001", func(e *irc.Event) {
		for _, channel := range config.Channels {
			con.Join(channel)
		}
	})

	con.AddCallback("JOIN", func(e *irc.Event) {
		if react(config.DefaultChattiness) {
			room, _ := inputSource(e.Raw, con.GetNick())
			con.Privmsg(room, randomSmiley())
			bumpLastReaction()
		}
	})

	// thin wrapper responsible for sending IRC messages
	privmsg := func(source string, nick string, message string, triggeredAt time.Time, prefixWithNick bool) {
		response := strings.TrimSpace(message)
		typingDelay(response, triggeredAt)
		if strings.HasPrefix(response, "/me ") {
			con.Action(source, strings.Replace(response, "/me ", "", 1))
		} else {
			if prefixWithNick {
				response = nick + ": " + response
			}
			con.Privmsg(source, response)
		}
	}

	con.AddCallback("PRIVMSG", func(e *irc.Event) {
		// response takes some work, running in a new thread
		go func(e *irc.Event) {
			start := time.Now()
			ownNick := con.GetNick()
			source, privateQuery := inputSource(e.Raw, ownNick)
			input := strings.TrimSpace(e.Message())

			if response := predefinedResponse(input); response != "" {
				bumpLastReaction()
				privmsg(source, e.Nick, response, start, !privateQuery)
				return // finish processing input
			}

			// fallback to markov-based generator
			words, seeds := processInput(input, !privateQuery)
			chattiness := calculateChattiness(input, ownNick, privateQuery)
			if react(chattiness) {
				bumpLastReaction()
				response := generateResponse(words, seeds, int(config.MaxResponseTries))
				prefixWithNick := chattiness == always && !privateQuery
				privmsg(source, e.Nick, response, start, prefixWithNick)
			}
		}(e)
	})

	quitEvent := regexp.MustCompile("^:([^!]+)!.+QUIT")
	con.AddCallback("QUIT", func(e *irc.Event) {
		go func(e *irc.Event) {
			ownNick := con.GetNick()
			quitNick := quitEvent.FindStringSubmatch(e.Raw)[1]
			if ownNick == quitNick && strings.Contains(e.Raw, "Ping timeout") {
				log.Println("Timeout detected, reconnecting to " + config.IrcServer)
				con.Reconnect()
			}
		}(e)
	})

	// proces termination signal triggers cleanup
	sc := make(chan os.Signal)
	signal.Notify(sc, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	go func() {
		sig := <-sc
		log.Warn("Received '", sig, "' signal, shutting down")
		exitCode := 0

		// persist corpus to disk
		log.Info("Saving the Corpus")
		defer pool.Close()
		corpus := pool.Get()
		defer corpus.Close()
		_, err := corpus.Do("SAVE")
		if err == nil {
			log.Info("Saved to dump.rdb")
		} else {
			log.Error("Unable to save Corpus: ", err)
			exitCode = 1
		}

		// disconnect
		if con.Connected() {
			log.Warn("Disconnecting from IRC")
			con.Quit()
			con.Disconnect()
		}
		os.Exit(exitCode)
	}()

	con.Loop()
	log.Panic("The IRC Loop finished prematurely")
}

// decides if there should be a reaction given current chattiness level
func react(chattiness float64) bool {
	return chattiness == always || (chattiness > rand.Float64() && withinReactionRate())
}

func bumpLastReaction() {
	atomic.StoreInt64(&lastReaction, time.Now().UnixNano())
}

func withinReactionRate() bool {
	return atomic.LoadInt64(&lastReaction) < time.Now().Add(-time.Duration(config.MinTimeBetweenReactions)*time.Second).UnixNano()
}

func inputSource(raw string, ownNick string) (string, bool) {
	channel := strings.Split(raw, " ")[2]
	privateQuery := channel == ownNick
	if privateQuery {
		channel = strings.Split(raw[1:], "!")[0]
	}
	return channel, privateQuery
}

func calculateChattiness(message string, currentBotNick string, privateQuery bool) float64 {
	chattiness := config.DefaultChattiness
	if privateQuery || strings.Contains(message, currentBotNick) || ownMention.MatchString(message) {
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
			log.Println("Using Dockerized Redis: " + env + "=" + host)
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

func typingDelay(text string, start time.Time) {
	durationSoFar := time.Since(start)
	// https://en.wikipedia.org/wiki/Words_per_minute
	typing := time.Duration((float64(len(text))/5)/float64(config.WordsPerMinute)*60)*time.Second - durationSoFar
	if config.Debug {
		log.Println("Calculating response took: " + fmt.Sprint(durationSoFar))
		log.Println("Remaining typing delay: " + fmt.Sprint(typing))
	}
	if typing > 0 {
		if config.Debug {
			log.Println("<sleeping for " + fmt.Sprint(typing) + ">")
		}
		time.Sleep(typing)
	}
}

func processInput(message string, learning bool) (words []string, seed [][]string) {
	words = parseInput(message)
	seed = createSeeds(words)
	if learning && int(config.ChainLength) < len(words) {
		addToCorpus(seed)
	}
	return
}

func parseInput(input string) []string {
	var (
		tokens = strings.Split(removeMention(input), " ")
		words  []string
	)
	for _, token := range tokens {
		if word := normalizeWord(token); len(word) > 0 {
			words = append(words, word)
		}
	}
	return append(words, stop)
}

// remove nickname-based prefix used for mentions
func removeMention(message string) string {
	if otherMention.MatchString(message) {
		return otherMention.ReplaceAllString(message, "")
	}
	return message
}

// normalizeWord removes various cruft from parsed text.
// The goal is to make corpus more uniform (no duplicate clusters for multiple versions of the same word)
func normalizeWord(word string) string {
	word = strings.TrimSpace(word)
	if !httpLink.MatchString(word) { // don't change URLs
		word = strings.ToLower(word)
		word = textCruft.ReplaceAllString(word, "$1")
		word = emoticonCruft.ReplaceAllString(word, "")
	}
	return word
}

// lookup for predefined (static) responses
func predefinedResponse(input string) string {
	message := removeMention(input)
	for key, val := range config.PredefinedResponses {
		if strings.Contains(message, key) {
			log.Println("Found PredefinedResponses match at key=" + key)
			// TODO: support evaluating val via external script
			return val
		}
	}
	return ""
}

func addToCorpus(seeds [][]string) {
	corpus := pool.Get()
	defer corpus.Close()
	for i, seed := range seeds {

		cut := len(seed) - 1
		head := seed[:cut]
		key := strings.Join(head, separator)
		value := seed[cut:][0]

		_, err := corpus.Do("SADD", key, value)
		if err != nil {
			redisErr(err)
			return
		}

		if config.Debug {
			log.Println("seed   #" + fmt.Sprint(i) + ":\t" + dump(seed))
			log.Println("key    #" + fmt.Sprint(i) + ":\t" + dump(head))
			chainValues, err := redis.Strings(corpus.Do("SMEMBERS", key))
			if err != nil {
				redisErr(err)
				return
			}
			log.Println("corpus #" + fmt.Sprint(i) + ":\t" + dump(chainValues))
		}
	}
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

func chainTransliterations(seeds [][]string) [][]string {
	var transliterations [][]string
	for _, chain := range seeds {
		var (
			asciiChain []string
			diff       = false
		)
		for _, word := range chain {
			ascii := unidecode.Unidecode(word)
			asciiChain = append(asciiChain, ascii)

			if ascii != word {
				diff = true
			}
		}
		if diff {
			transliterations = append(transliterations, asciiChain)
		}
	}
	return transliterations
}

func generateResponse(input []string, seeds [][]string, triesLeft int) string {

	if config.Debug {
		log.Println("Generating response for input: " + dump(input))
	}

	var wg sync.WaitGroup
	var mtx sync.Mutex
	var responset = make(uniqueTexts)
	for _, seed := range append(seeds, chainTransliterations(seeds)...) {
		wg.Add(1)
		go func(seed []string) {
			defer wg.Done()
			for i := 0; i < int(config.ChainsToTry); i++ {
				if response := randomBranch(seed); !isEmpty(response) && !contains(seed, response) {
					mtx.Lock()
					responset[response] = struct{}{}
					mtx.Unlock()
				}
				runtime.Gosched()
			}
		}(seed)
	}
	wg.Wait()

	responses := normalizeResponseChains(responset)
	count := len(responses)

	if config.Debug {
		log.Println("Found " + fmt.Sprint(len(responses)) + " potential responses")
		if count > 0 {
			log.Println(dump(responses))
		}
	}

	var response string
	if count >= int(config.MinResponsePool) {
		response = responses[rand.Intn(count)]
		response = response + " " + randomSmiley()
	} else if triesLeft > 0 {
		triesLeft--
		try := int(config.MaxResponseTries) - triesLeft
		power := try * try * try // * try
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

func contains(items []string, item string) bool {
	for _, oldItem := range items {
		if item == oldItem {
			return true
		}
	}
	return false
}

func randomBranch(words []string) string {
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

	response = removeBlacklistedWords(response)

	return strings.Join(response, " ")
}

func randomWord(key string) string {
	corpus := pool.Get()
	defer corpus.Close()
	value, err := redis.String(corpus.Do("SRANDMEMBER", key))
	if err == nil || err == redis.ErrNil {
		return value
	}
	redisErr(err)
	return stop
}

func randomChain() []string {
	corpus := pool.Get()
	defer corpus.Close()
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
		input = randomChain()[:1]
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
				defer wg.Done()
				for _, mutation := range createSeeds(mutateChain(word, randomChain())) {
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

func normalizeResponseChains(texts uniqueTexts) []string {
	var result []string

	if len(texts) == 0 {
		return []string{}
	}

	if config.Debug {
		log.Println("Normalizing " + fmt.Sprint(len(texts)) + " unique responses")
	}

	// calculate lengths
	l := map[int]struct{}{}
	for text := range texts {
		l[len(text)] = struct{}{}
	}
	lengths := make([]int, 0, len(l))
	for k := range l {
		lengths = append(lengths, k)
	}

	// drop bottom half (below median)
	threshold := median(lengths)
	for text := range texts {
		if len(text) >= threshold {
			result = append(result, text)
		}
	}
	if config.Debug {
		log.Println("Discarded responses <= median of " + fmt.Sprint(threshold) + " characters")
	}

	if isChainEmpty(result) {
		result = []string{}
	}

	return result
}

func median(numbers []int) int {
	sort.Ints(numbers)

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
	log.Errorf("[redis error]: %v\n", err.Error())
}

func check(e error, message string) {
	if e != nil {
		if message != "" {
			log.Println(message)
		}
		log.Panicln(e)
	}
}
