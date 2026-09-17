package dashboard

import (
	"errors"
	"net"
	"strconv"
	"strings"
	"testing"
)

// .
// .
// .
// .
// .
func TestAConfiguredPortReservesItsLoopbackCompanionOrRefuses(t *testing.T) {
	ip := nonLoopbackIPv4(t)
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("no loopback to probe a free port on")
	}
	_, port, _ := net.SplitHostPort(probe.Addr().String())
	probe.Close()
	configured, _ := strconv.Atoi(port)

	// .
	ln, loopback, err := listenDashboard(net.JoinHostPort(ip, port), configured, net.Listen)
	if err != nil {
		t.Fatal(err)
	}
	if loopback == nil || loopback.Addr().String() != "127.0.0.1:"+port {
		t.Fatalf("a configured port did not reserve its loopback companion: %v", loopback)
	}
	ln.Close()
	loopback.Close()

	// .
	// .
	occupied := func(network, address string) (net.Listener, error) {
		if strings.HasPrefix(address, "127.0.0.1:") {
			return nil, errors.New("occupied")
		}
		return net.Listen(network, address)
	}
	ln, loopback, err = listenDashboard(net.JoinHostPort(ip, port), configured, occupied)
	if ln != nil {
		ln.Close()
	}
	if loopback != nil {
		loopback.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "loopback companion 127.0.0.1:"+port) || ln != nil || loopback != nil {
		t.Fatalf("a bind whose loopback companion is occupied was not refused: err=%v ln=%v loopback=%v", err, ln, loopback)
	}

	// .
	ln, loopback, err = listenDashboard("127.0.0.1:"+port, configured, net.Listen)
	if err != nil {
		t.Fatal(err)
	}
	ln.Close()
	if loopback != nil {
		loopback.Close()
		t.Fatal("a loopback bind reserved a companion it does not need")
	}
}
