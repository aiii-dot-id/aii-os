//go:build !((linux && !android) || (darwin && !ios) || windows)

package machine

import "context"

type storeOptions struct{}

type storeService struct{ opts storeOptions }

func (s *Server) openStore(context.Context)        { s.store = &storeService{opts: s.opts.store} }
func (s *Server) closeStore()                      { s.store = nil }
func (s *Server) storeLinked(string)               {}
func (s *Server) storeStatusLocked(*Status, *link) {}

func (s *Server) letGo(f func(*State)) error {
	return s.updateState(func(st *State) error { f(st); return nil })
}

func (s *Server) dispatchStore(*link, message) (message, func(), bool) {
	return message{}, nil, false
}
