package main

import (
	"os"
	"reflect"
	"testing"
)

func init() {
	// run against config template
	loadConfig("meowkov.conf.template")
}

func TestParseInput_standard(t *testing.T) {
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
	chattiness := calculateChattiness("foo bar one two", "nickname")
	if chattiness != config.DefaultChattiness {
		t.Error("calculateChattiness should return DefaultChattiness if bot's nickname is not mentioned")
	}
	chattiness = calculateChattiness("foo bar nickname one two", "nickname")
	if chattiness != always {
		t.Error("calculateChattiness should return 1.0 if nickname is mentioned")
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
		[]string{"1", "2", "3"},
		[]string{"2", "3", "4"},
		[]string{"3", "4", "5"},
		[]string{"4", "5", "6"},
	}
	output := createSeeds(input)
	if !reflect.DeepEqual(output, expected) {
		t.Error("createSeeds returns incorrect chain groups")
	}
}
