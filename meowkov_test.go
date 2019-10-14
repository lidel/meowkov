package main

import (
	"fmt"
	"os"
	"reflect"
	"sort"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	// run against config template
	// flag.Parse()
	loadConfig("meowkov.conf.template")
	os.Exit(m.Run())
}

func TestProcessInput(t *testing.T) {
	input := "1 2 3 4 5 6"
	expWords := []string{"1", "2", "3", "4", "5", "6", stop}
	expSeeds := [][]string{
		{"1", "2", "3"},
		{"2", "3", "4"},
		{"3", "4", "5"},
		{"4", "5", "6"},
		{"5", "6", stop},
	}
	words, seeds := processInput(input, false)
	if !reflect.DeepEqual(words, expWords) {
		t.Error("processInput words do not match expected value")
	}
	if !reflect.DeepEqual(seeds, expSeeds) {
		t.Error("processInput seeds do not match expected value")
	}

}

func TestParseInput(t *testing.T) {
	test := func(input string, expected []string) {
		words := parseInput(input)
		if !reflect.DeepEqual(words, expected) {
			t.Error("parseInput words " + dump(words) + " do not match expected " + dump(expected))
		}
	}

	// plain message
	input := "1 2 3"
	expectedWords := []string{"1", "2", "3", stop}
	test(input, expectedWords)

	// remove mentions present at the beginning
	input = config.BotName + ": 1 2 3"
	test(input, expectedWords)
	input = config.BotName + ", 1 2 3"
	test(input, expectedWords)

	// remove BotName if used as mention at the beginning
	input = config.BotName + ": look: 2 3"
	expectedWords = []string{"look:", "2", "3", stop}
	test(input, expectedWords)
	input = config.BotName + ", look: 2 3"
	test(input, expectedWords)

	// do not remove BotName if in the middle
	input = "1 " + config.BotName + " 2 3"
	expectedWords = []string{"1", config.BotName, "2", "3", stop}
	test(input, expectedWords)

	// lowercase input with exception of URLs
	input = "PlAy PiAno https://yt.aergia.eu/#v=T0rs3R4E1Sk&t=23;30"
	expectedWords = []string{"play", "piano", "https://yt.aergia.eu/#v=T0rs3R4E1Sk&t=23;30", stop}
	test(input, expectedWords)
}

func TestNormalizeWord(t *testing.T) {
	test := func(input string, expected string) {
		normalized := normalizeWord(input)
		if normalized != expected {
			t.Error("normalizeWord result >" + normalized + "< does not match expected >" + expected + "<")
		}
	}

	// strip spaces and lowercase input
	test(" CaSe ", "case")

	// strip spaces but no not lowercase URL
	test("  https://yt.aergia.eu/#v=T0rs3R4E1Sk&t=23;3 ", "https://yt.aergia.eu/#v=T0rs3R4E1Sk&t=23;3")

	// remove " from beginning and/or end
	test(" \"foo", "foo")
	test(" foo\" ", "foo")
	test(" \"foo\" ", "foo")
	test(" f\"oo ", "f\"oo")

	// remove ' from beginning and/or end
	test(" 'foo", "foo")
	test(" foo' ", "foo")
	test(" 'foo' ", "foo")
	test(" f'oo ", "f'oo")

	// remove ( and ) from beginning and/or end
	test(" (foo)", "foo")
	test(" (foo ", "foo")
	test(" foo) ", "foo")
	test(" f(oo ", "f(oo")

	// remove [ and ] from beginning and/or end
	test(" [foo]", "foo")
	test(" [foo ", "foo")
	test(" foo] ", "foo")
	test(" f[oo ", "f[oo")

	// remove ? and ! from end only
	test(" foo? ", "foo")
	test(" foo! ", "foo")
	test(" foo!?!?!? ", "foo")
	test(" foo!?bar ", "foo!?bar")
	test(" “foo” ", "foo")
	test(" „foo” ", "foo")
	test(" :-(((((( ", "")
	test(" :((( ", "")
	test(" ;[[ ", "")
	test(" :-^-< ", "")
	test(" :\"< ", "")
	test(" ;'< ", "")
	test(" :'D ", "")
	test(" :-Pppp ", "")
}

func TestGetRedisServer(t *testing.T) {
	config.RedisServer = "foo:1234"
	if getRedisServer() != "foo:1234" {
		t.Error("redis address should be loaded from config")
	}
	os.Setenv("REDIS_PORT_1234_TCP_ADDR", "bar")
	if getRedisServer() != "bar:1234" {
		t.Error("redis address should come from ENV when run in docker")
	}
}

func TestIsEmpty(t *testing.T) {
	problem := !isEmpty("") || !isEmpty(stop)
	if problem {
		t.Error("Empty string should be empty ;-)")
	}
}

func TestIsChainEmpty(t *testing.T) {
	problem := !isChainEmpty([]string{stop}) || !isChainEmpty([]string{}) || !isChainEmpty([]string{""})
	if problem {
		t.Error("Empty slice should be empty ;-)")
	}
}

func TestCalculateChattiness(t *testing.T) {
	privateQuery := false
	chattiness := calculateChattiness("foo bar one two", "nickname", privateQuery)
	if chattiness != config.DefaultChattiness {
		t.Error("calculateChattiness should return DefaultChattiness if bot's nickname is not mentioned")
	}
	chattiness = calculateChattiness("foo bar nickname one two", "nickname", privateQuery)
	if chattiness != always {
		t.Error("calculateChattiness should return 1.0 if nickname is mentioned")
	}
	privateQuery = true
	chattiness = calculateChattiness("foo bar one two", "nickname", privateQuery)
	if chattiness != always {
		t.Error("calculateChattiness should return 1.0 if input is from a private query")
	}

}

func TestInputSource(t *testing.T) {
	rawChannelMsg := ":foo!~bar@unaffiliated/foobar PRIVMSG #test :foo"
	rawPrivateMsg := ":foo!~bar@unaffiliated/foobar PRIVMSG meowkov :foo"
	ownNick := "meowkov"

	source, priv := inputSource(rawChannelMsg, ownNick)
	if source != "#test" || priv {
		t.Error("inputSource should return channel")
	}
	source, priv = inputSource(rawPrivateMsg, ownNick)
	if source != "foo" || !priv {
		t.Error("inputSource should return private query")
	}

}

func TestCreateSeeds(t *testing.T) {
	input := []string{"1", "2", "3", "4", "5", "6"}
	expected := [][]string{
		{"1", "2", "3"},
		{"2", "3", "4"},
		{"3", "4", "5"},
		{"4", "5", "6"},
	}
	output := createSeeds(input)
	if !reflect.DeepEqual(output, expected) {
		t.Error("createSeeds returns incorrect chain groups")
	}
}

func TestAppendTransliterations(t *testing.T) {
	test := func(input [][]string, expected [][]string) {
		output := chainTransliterations(input)
		if !reflect.DeepEqual(output, expected) {
			t.Error("chainTransliterations returns incorrect chain groups: " + fmt.Sprintf("%#v", output) + ", expected: " + fmt.Sprintf("%#v", expected))
		}
	}

	test([][]string{
		{"2", "3", "4"},
		{"3", "ź", "5"},
		{"4", "5", "żółć"},
	}, [][]string{
		{"3", "z", "5"},
		{"4", "5", "zolc"},
	})

	test([][]string{
		{"2", "3", "4"},
		{"3", "4", "5"},
	}, [][]string(nil))
}

func TestContains(t *testing.T) {
	items := []string{"1", "2", "3"}
	test := func(items []string, item string, expected bool) {
		if contains(items, item) != expected {
			t.Error("contains(" + dump(items) + "," + item + ") should return " + fmt.Sprint(expected))
		}
	}
	test(items, "1", true)
	test(items, "2", true)
	test(items, "3", true)
	test(items, "A", false)

}

func TestMutateChain(t *testing.T) {
	input := []string{"1", "2"}
	word := "A"
	expected := []string{"A", "1", "A", "2", "A"}
	output := mutateChain(word, input)
	if !reflect.DeepEqual(output, expected) {
		t.Error("mutateChain should return " + dump(expected))
	}
}

func TestRandomSmiley(t *testing.T) {
	if !contains(config.Smileys, randomSmiley()) {
		t.Error("randomSmiley should return random item from the list in config file")
	}
}

func TestRemoveBlacklistedWords(t *testing.T) {
	blacklistOrig := config.Blacklist
	dontEndOrig := config.DontEndWith

	config.Blacklist = []string{"2"}
	config.DontEndWith = []string{"5", "6"}
	input := []string{"1", "2", "3", "4", "5", "6"}
	expected := []string{"1", "3", "4"}
	output := removeBlacklistedWords(input)
	if !reflect.DeepEqual(output, expected) {
		t.Error("removeBlacklistedWords should return " + dump(expected))
	}

	config.Blacklist = blacklistOrig
	config.DontEndWith = dontEndOrig
}

func TestMedian(t *testing.T) {
	input := []int{6, 2, 3, 4, 5, 1}
	expected := 3
	output := median(input)
	if output != expected {
		t.Error("median should return " + fmt.Sprint(expected) + " but got " + fmt.Sprint(output))
	}
}

func TestNormalizeResponseChains(t *testing.T) {
	input := make(uniqueTexts)
	input["1"] = struct{}{}
	input["22"] = struct{}{}
	input["333"] = struct{}{}
	input["4444"] = struct{}{}
	input["55555"] = struct{}{}
	input["666666"] = struct{}{}
	expected := []string{"333", "4444", "55555", "666666"}
	output := normalizeResponseChains(input)
	sort.Strings(output)
	if !reflect.DeepEqual(output, expected) {
		t.Error("normalizeResponseChains should return " + dump(expected) + " but got " + dump(output))
	}
}

func TestDump(t *testing.T) {
	input := []string{"1", "2", "3"}
	expected := `["1", "2", "3"]`
	output := dump(input)
	if expected != output {
		t.Error("dump should return " + expected + " but got " + output)
	}
}

func TestTypingDelay(t *testing.T) {
	start := time.Now()
	typingDelay("fooo bar", time.Unix(start.Unix()-1, 0))
	end := time.Now()
	if end.Sub(start) > 1*time.Second {
		t.Error("typingDelay should occur if response took long time to generate")
	}
}
