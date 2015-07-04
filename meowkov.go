package main

import (
	"github.com/thoj/go-ircevent"
)

var roomName = "#meowkov"

func main() {

	con := irc.IRC("meowkov", "meowkov")
	con.UseTLS = true
	con.Connect("chat.freenode.net:7000")

	con.AddCallback("001", func(e *irc.Event) {
		con.Join(roomName)
	})

	con.AddCallback("JOIN", func(e *irc.Event) {
		con.Privmsg(roomName, "Hello! I am a friendly IRC bot who will violate everything you say.")
	})

	con.AddCallback("PRIVMSG", func(e *irc.Event) {
		con.Privmsg(roomName, e.Message())
	})

	con.Loop()
}
