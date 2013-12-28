package main

import (
	"io"
	. "launchpad.net/gocheck"
	"launchpad.net/wlmetaserver/wlms/packet"
	"log"
	"testing"
	"time"
)

// Hook up gocheck into the gotest runner.
func Test(t *testing.T) { TestingT(t) }

type EndToEndSuite struct{}

var _ = Suite(&EndToEndSuite{})

type Matching string

func writeDataToConnection(conn FakeConn, data ...string) {
	go func() {
		for _, d := range data {
			conn.Write([]byte(d))
			time.Sleep(10 * time.Millisecond)
		}
	}()
}

func SendPacket(f FakeConn, data ...interface{}) {
	f.ServerWriter().Write(packet.New(data...))
}

func ExpectClosed(c *C, f FakeConn) {
	c.Assert(f.GotClosed(), Equals, true)
}

func SetupServer(c *C, nClients int) (*Server, []FakeConn) {
	log.SetFlags(log.Lshortfile)
	db := NewInMemoryDb()
	db.AddUser("SirVer", "123456", SUPERUSER)
	db.AddUser("otto", "ottoiscool", REGISTERED)

	acceptingConnections := make(chan io.ReadWriteCloser, 20)
	cons := make([]FakeConn, nClients)
	for i := range cons {
		cons[i] = NewFakeConn(c)
		acceptingConnections <- cons[i]
	}
	return CreateServerUsing(acceptingConnections, db), cons
}

func ExpectPacket(c *C, f FakeConn, expected ...interface{}) {
	timer := time.NewTimer(20 * time.Millisecond)
	select {
	case packet := <-f.Packets:
		c.Check(len(packet.RawData), Equals, len(expected))
		for i := 0; i < len(packet.RawData); i += 1 {
			switch expect := expected[i].(type) {
			case Matching:
				c.Check(packet.RawData[i], Matches, string(expect))
				continue
			default:
				c.Check(packet.RawData[i], Equals, expected[i])
			}
		}
	case <-timer.C:
		c.Errorf("No packet arrived, though we expected one.")
	}
}

func ExpectLoginAsUnregisteredWorks(c *C, f FakeConn, name string) {
	SendPacket(f, "LOGIN", 0, name, "bzr1234[trunk]", false)
	ExpectPacket(c, f, "LOGIN", name, "UNREGISTERED")
	ExpectPacket(c, f, "TIME", Matching("\\d+"))
	ExpectPacket(c, f, "CLIENTS_UPDATE")
}

func ExpectLoginAsRegisteredWorks(c *C, f FakeConn, name, password string) {
	SendPacket(f, "LOGIN", 0, name, "bzr1234[trunk]", true, password)
	ExpectPacket(c, f, "LOGIN", name, Matching("(REGISTERED|SUPERUSER)"))
	ExpectPacket(c, f, "TIME", Matching("\\d+"))
	ExpectPacket(c, f, "CLIENTS_UPDATE")
}

func ExpectServerToShutdownCleanly(c *C, server *Server) {
	server.Shutdown()
	server.WaitTillShutdown()
	c.Assert(server.NrClients(), Equals, 0)
}

// Test Packet decoding {{{
func (s *EndToEndSuite) TestSimplePacket(c *C) {
	conn := NewFakeConn(c)
	writeDataToConnection(conn, "\x00\x07aaaa\x00")
	ExpectPacket(c, conn, "aaaa")
}

func (s *EndToEndSuite) TestSimplePacket1(c *C) {
	conn := NewFakeConn(c)
	writeDataToConnection(conn, "\x00\x10aaaa\x00bbb\x00cc\x00d\x00")
	ExpectPacket(c, conn, "aaaa", "bbb", "cc", "d")
}

func (s *EndToEndSuite) TestTwoPacketsInOneRead(c *C) {
	conn := NewFakeConn(c)
	writeDataToConnection(conn, "\x00\x07aaaa\x00\x00\x07aaaa\x00")
	ExpectPacket(c, conn, "aaaa")
	ExpectPacket(c, conn, "aaaa")
}

func (p *EndToEndSuite) TestFragmentedPackets(c *C) {
	conn := NewFakeConn(c)
	writeDataToConnection(conn, "\x00\x0aCLI", "ENTS\x00\x00\x0a", "CLIENTS\x00\x00\x08")
	ExpectPacket(c, conn, "CLIENTS")
	ExpectPacket(c, conn, "CLIENTS")
}

// }}}

// Test Login {{{
func (s *EndToEndSuite) TestRegisteredUserIncorrectPassword(c *C) {
	server, clients := SetupServer(c, 2)

	SendPacket(clients[0], "LOGIN", 0, "SirVer", "bzr1234[trunk]", true, "23456")
	ExpectPacket(c, clients[0], "ERROR", "LOGIN", "WRONG_PASSWORD")

	ExpectServerToShutdownCleanly(c, server)
}

func (s *EndToEndSuite) TestRegisteredUserNotExisting(c *C) {
	server, clients := SetupServer(c, 2)

	SendPacket(clients[0], "LOGIN", 0, "bluba", "bzr1234[trunk]", true, "123456")
	ExpectPacket(c, clients[0], "ERROR", "LOGIN", "WRONG_PASSWORD")

	ExpectServerToShutdownCleanly(c, server)
}

func (s *EndToEndSuite) TestLoginAnonymouslyWorks(c *C) {
	server, clients := SetupServer(c, 1)

	SendPacket(clients[0], "LOGIN", 0, "testuser", "bzr1234[trunk]", false)

	ExpectPacket(c, clients[0], "LOGIN", "testuser", "UNREGISTERED")
	ExpectPacket(c, clients[0], "TIME", Matching("\\d+"))
	ExpectPacket(c, clients[0], "CLIENTS_UPDATE")
	clients[0].Close()

	time.Sleep(5 * time.Millisecond)
	c.Assert(server.NrClients(), Equals, 0)

	ExpectServerToShutdownCleanly(c, server)
	ExpectClosed(c, clients[0])
}

func (s *EndToEndSuite) TestLoginUnknownProtocol(c *C) {
	server, clients := SetupServer(c, 1)

	SendPacket(clients[0], "LOGIN", 10, "testuser", "bzr1234[trunk]", false)
	ExpectPacket(c, clients[0], "ERROR", "LOGIN", "UNSUPPORTED_PROTOCOL")

	time.Sleep(5 * time.Millisecond)
	c.Assert(server.NrClients(), Equals, 0)

	ExpectServerToShutdownCleanly(c, server)
}

func (s *EndToEndSuite) TestLoginWithKnownUserName(c *C) {
	server, clients := SetupServer(c, 1)

	SendPacket(clients[0], "LOGIN", 0, "SirVer", "bzr1234[trunk]", false)
	ExpectPacket(c, clients[0], "LOGIN", "SirVer1", "UNREGISTERED")
	ExpectPacket(c, clients[0], "TIME", Matching("\\d+"))
	ExpectPacket(c, clients[0], "CLIENTS_UPDATE")

	ExpectServerToShutdownCleanly(c, server)
}

func (s *EndToEndSuite) TestLoginOneWasAlreadyThere(c *C) {
	server, clients := SetupServer(c, 2)

	ExpectLoginAsUnregisteredWorks(c, clients[0], "testuser")

	SendPacket(clients[1], "LOGIN", 0, "testuser", "bzr1234[trunk]", false)
	ExpectPacket(c, clients[1], "LOGIN", "testuser1", "UNREGISTERED")
	ExpectPacket(c, clients[1], "TIME", Matching("\\d+"))
	ExpectPacket(c, clients[1], "CLIENTS_UPDATE")

	ExpectPacket(c, clients[0], "CLIENTS_UPDATE")

	ExpectServerToShutdownCleanly(c, server)
}

func (s *EndToEndSuite) TestRegisteredUserCorrectPassword(c *C) {
	server, clients := SetupServer(c, 2)

	SendPacket(clients[0], "LOGIN", 0, "SirVer", "bzr1234[trunk]", true, "123456")
	ExpectPacket(c, clients[0], "LOGIN", "SirVer", "SUPERUSER")
	ExpectPacket(c, clients[0], "TIME", Matching("\\d+"))
	ExpectPacket(c, clients[0], "CLIENTS_UPDATE")

	SendPacket(clients[1], "LOGIN", 0, "otto", "bzr1234[trunk]", true, "ottoiscool")
	ExpectPacket(c, clients[1], "LOGIN", "otto", "REGISTERED")
	ExpectPacket(c, clients[1], "TIME", Matching("\\d+"))
	ExpectPacket(c, clients[1], "CLIENTS_UPDATE")

	ExpectPacket(c, clients[0], "CLIENTS_UPDATE")

	ExpectServerToShutdownCleanly(c, server)
}

func (s *EndToEndSuite) TestRegisteredUserAlreadyLoggedIn(c *C) {
	server, clients := SetupServer(c, 2)

	ExpectLoginAsRegisteredWorks(c, clients[0], "SirVer", "123456")

	SendPacket(clients[1], "LOGIN", 0, "SirVer", "bzr1234[trunk]", true, "123456")
	ExpectPacket(c, clients[1], "ERROR", "LOGIN", "ALREADY_LOGGED_IN")

	ExpectServerToShutdownCleanly(c, server)
}

/// }}}
// Test Disconnect {{{
func (e *EndToEndSuite) TestDisconnect(c *C) {
	server, clients := SetupServer(c, 2)

	ExpectLoginAsUnregisteredWorks(c, clients[0], "bert")
	ExpectLoginAsRegisteredWorks(c, clients[1], "otto", "ottoiscool")

	ExpectPacket(c, clients[0], "CLIENTS_UPDATE")
	SendPacket(clients[0], "DISCONNECT", "Gotta fly now!")
	clients[0].Close()

	ExpectPacket(c, clients[1], "CLIENTS_UPDATE")

	c.Assert(server.NrClients(), Equals, 1)
	ExpectServerToShutdownCleanly(c, server)
}

// }}}
// Test Chat {{{
func (e *EndToEndSuite) TestChat(c *C) {
	server, clients := SetupServer(c, 2)

	ExpectLoginAsUnregisteredWorks(c, clients[0], "bert")
	ExpectLoginAsUnregisteredWorks(c, clients[1], "ernie")

	ExpectPacket(c, clients[0], "CLIENTS_UPDATE")

	// Send public messages.
	SendPacket(clients[0], "CHAT", "hello there", "")
	ExpectPacket(c, clients[0], "CHAT", "bert", "hello there", "public")
	ExpectPacket(c, clients[1], "CHAT", "bert", "hello there", "public")
	SendPacket(clients[0], "CHAT", "hello <rt>there</rt>\nhow<rtdoyoudo", "")
	ExpectPacket(c, clients[0], "CHAT", "bert", "hello &lt;rt>there&lt;/rt>\nhow&lt;rtdoyoudo", "public")
	ExpectPacket(c, clients[1], "CHAT", "bert", "hello &lt;rt>there&lt;/rt>\nhow&lt;rtdoyoudo", "public")

	// Send private messages.
	SendPacket(clients[0], "CHAT", "hello there", "ernie")
	SendPacket(clients[0], "CHAT", "hello <rt>there</rt>\nhow<rtdoyoudo", "ernie")
	ExpectPacket(c, clients[1], "CHAT", "bert", "hello there", "private")
	ExpectPacket(c, clients[1], "CHAT", "bert", "hello &lt;rt>there&lt;/rt>\nhow&lt;rtdoyoudo", "private")

	ExpectServerToShutdownCleanly(c, server)
}

// }}}
// Test Faulty Communication {{{
func (e *EndToEndSuite) TestUnknownPacket(c *C) {
	server, clients := SetupServer(c, 1)
	SendPacket(clients[0], "BLUMBAQUATSCH")
	ExpectPacket(c, clients[0], "ERROR", "GARBAGE_RECEIVED", "INVALID_CMD")

	time.Sleep(5 * time.Millisecond)
	ExpectClosed(c, clients[0])

	ExpectServerToShutdownCleanly(c, server)
}

func (e *EndToEndSuite) TestWrongArgumentsInPacket(c *C) {
	server, clients := SetupServer(c, 1)
	SendPacket(clients[0], "LOGIN", "hi")
	ExpectPacket(c, clients[0], "ERROR", "LOGIN", "Invalid integer: 'hi'")

	time.Sleep(5 * time.Millisecond)
	ExpectClosed(c, clients[0])

	ExpectServerToShutdownCleanly(c, server)
}

func (s *EndToEndSuite) TestClientCanTimeout(c *C) {
	server, clients := SetupServer(c, 1)

	server.SetClientSendingTimeout(1 * time.Millisecond)

	ExpectLoginAsUnregisteredWorks(c, clients[0], "testuser")

	time.Sleep(5 * time.Millisecond)
	ExpectPacket(c, clients[0], "DISCONNECT", "CLIENT_TIMEOUT")
	time.Sleep(5 * time.Millisecond)

	ExpectClosed(c, clients[0])
	c.Assert(server.NrClients(), Equals, 0)

	ExpectServerToShutdownCleanly(c, server)
}

// }}}
// Test Pinging {{{
func (s *EndToEndSuite) TestRegularPingCycle(c *C) {
	server, clients := SetupServer(c, 1)

	server.SetPingCycleTime(5 * time.Millisecond)

	ExpectLoginAsUnregisteredWorks(c, clients[0], "testuser")

	// Regular Ping cycle
	time.Sleep(6 * time.Millisecond)
	ExpectPacket(c, clients[0], "PING")
	SendPacket(clients[0], "PONG")
	time.Sleep(6 * time.Millisecond)
	ExpectPacket(c, clients[0], "PING")

	// Regular packages are as good as a Pong.
	SendPacket(clients[0], "CHAT", "hello there", "")
	ExpectPacket(c, clients[0], "CHAT", "testuser", "hello there", "public")
	time.Sleep(6 * time.Millisecond)
	ExpectPacket(c, clients[0], "PING")
	SendPacket(clients[0], "CHAT", "hello there", "")
	ExpectPacket(c, clients[0], "CHAT", "testuser", "hello there", "public")
	time.Sleep(6 * time.Millisecond)
	ExpectPacket(c, clients[0], "PING")

	// Timeout
	time.Sleep(15 * time.Millisecond)
	ExpectPacket(c, clients[0], "DISCONNECT", "CLIENT_TIMEOUT")
	time.Sleep(1 * time.Millisecond)
	ExpectClosed(c, clients[0])

	c.Assert(server.NrClients(), Equals, 0)

	ExpectServerToShutdownCleanly(c, server)
}

// }}}

func (s *EndToEndSuite) TestMotd(c *C) {
	server, clients := SetupServer(c, 3)

	ExpectLoginAsRegisteredWorks(c, clients[0], "SirVer", "123456")
	ExpectLoginAsRegisteredWorks(c, clients[1], "otto", "ottoiscool")
	ExpectPacket(c, clients[0], "CLIENTS_UPDATE")

	// Check Superuser setting motd.
	SendPacket(clients[0], "MOTD", "Schnulz is cool!")
	ExpectPacket(c, clients[0], "CHAT", "", "Schnulz is cool!", "system")
	ExpectPacket(c, clients[1], "CHAT", "", "Schnulz is cool!", "system")

	// Check normal user setting motd.
	SendPacket(clients[1], "MOTD", "Schnulz is cool!")
	ExpectPacket(c, clients[1], "ERROR", "MOTD", "DEFICIENT_PERMISSION")
	// This will not close your connection.

	// Login and you'll receive a motd.
	ExpectLoginAsUnregisteredWorks(c, clients[2], "bert")
	ExpectPacket(c, clients[2], "CHAT", "", "Schnulz is cool!", "system")

	ExpectPacket(c, clients[0], "CLIENTS_UPDATE")
	ExpectPacket(c, clients[1], "CLIENTS_UPDATE")

	ExpectServerToShutdownCleanly(c, server)
}
