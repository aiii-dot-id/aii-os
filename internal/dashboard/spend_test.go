package dashboard

import (
	"context"
	"reflect"
	"testing"
)

// The page asks for the language model's spend through the dashboard's own
// query, and the days it names are the days it is answered for: today
// alone when it names none. The answer carries the request it belongs to.
func TestThePageAsksForTheSpendOfTheDaysItNames(t *testing.T) {
	asked := make(chan int, 2)
	h := &WSHandler{GetSpend: func(days int) (*SpendView, error) {
		asked <- days
		return &SpendView{Zone: "UTC", Days: []SpendDay{{Day: "2026-10-22", Tokens: 3}}}, nil
	}}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	conn := dialWS(t, addr)

	sendMsg(t, conn, ClientMessage{RequestID: "spend-7", Type: "query", Query: "spend", Days: 7})
	got := drainUntil(t, conn, "spend")
	if got.RequestID != "spend-7" || got.Spend == nil || len(got.Spend.Days) != 1 || got.Spend.Days[0].Tokens != 3 {
		t.Fatalf("the spend query answered %+v", got)
	}
	sendMsg(t, conn, ClientMessage{Type: "query", Query: "spend"})
	drainUntil(t, conn, "spend")
	if days := []int{<-asked, <-asked}; !reflect.DeepEqual(days, []int{7, 1}) {
		t.Fatalf("the view was asked for %v days; want 7, then today alone", days)
	}
}
