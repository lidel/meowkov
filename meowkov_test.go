package main

import (
	"os"
	"testing"
)

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
