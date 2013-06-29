package store

import (
	"encoding/json"
	"fmt"
	"path"
	"time"
)

// global store
var s *Store

// CONSTANTS
const (
	ERROR = -1 + iota
	SET
	DELETE
	GET
)

var PERMANENT = time.Unix(0, 0)

type Store struct {
	// use the build-in hash map as the key-value store structure
	Nodes map[string]Node `json:"nodes"`

	// the string channel to send messages to the outside world
	// now we use it to send changes to the hub of the web service
	messager *chan string
}

type Node struct {
	Value string `json:"value"`

	// if the node is a permanent one the ExprieTime will be Unix(0,0)
	// Otherwise after the expireTime, the node will be deleted
	ExpireTime time.Time `json:"expireTime"`

	// a channel to update the expireTime of the node
	update chan time.Time `json:"-"`
}

type Response struct {
	Action   int    `json:"action"`
	Key      string `json:"key"`
	OldValue string `json:"oldValue"`
	NewValue string `json:"newValue"`

	// if the key existed before the action, this field should be true
	// if the key did not exist before the action, this field should be false
	Exist bool `json:"exist"`

	Expiration time.Time `json:"expiration"`

	Index uint64 `json:"index"`
}

func init() {
	s = createStore()
	s.messager = nil
}

// make a new stroe
func createStore() *Store {
	s := new(Store)
	s.Nodes = make(map[string]Node)
	return s
}

// return a pointer to the store
func GetStore() *Store {
	return s
}

// set the messager of the store
func (s *Store) SetMessager(messager *chan string) {
	s.messager = messager
}

// set the key to value, return the old value if the key exists
func Set(key string, value string, expireTime time.Time, index uint64) ([]byte, error) {

	key = path.Clean(key)

	var isExpire bool = false

	isExpire = !expireTime.Equal(PERMANENT)

	// when the slow follower receive the set command
	// the key may be expired, we should not add the node
	// also if the node exist, we need to delete the node
	if isExpire && expireTime.Sub(time.Now()) < 0 {
		return Delete(key, index)
	}

	// get the node
	node, ok := s.Nodes[key]

	if ok {
		// if node is not permanent before
		// update its expireTime
		if !node.ExpireTime.Equal(PERMANENT) {

			node.update <- expireTime

		} else {
			// if we want the permanent node to have expire time
			// we need to create a chan and create a go routine
			if isExpire {
				node.update = make(chan time.Time)
				go expire(key, node.update, expireTime)
			}
		}

		// update the information of the node
		s.Nodes[key] = Node{value, expireTime, node.update}

		resp := Response{SET, key, node.Value, value, true, expireTime, index}

		msg, err := json.Marshal(resp)

		notify(resp)

		// send to the messager
		if s.messager != nil && err == nil {

			*s.messager <- string(msg)
		}

		return msg, err

		// add new node
	} else {

		update := make(chan time.Time)

		s.Nodes[key] = Node{value, expireTime, update}

		if isExpire {
			go expire(key, update, expireTime)
		}

		resp := Response{SET, key, "", value, false, expireTime, index}

		msg, err := json.Marshal(resp)

		// nofity the watcher
		notify(resp)

		// notify the web interface
		if s.messager != nil && err == nil {

			*s.messager <- string(msg)
		}

		return msg, err
	}
}

// should be used as a go routine to delete the key when it expires
func expire(key string, update chan time.Time, expireTime time.Time) {
	duration := expireTime.Sub(time.Now())

	for {
		select {
		// timeout delete the node
		case <-time.After(duration):
			node, ok := s.Nodes[key]
			if !ok {
				return
			} else {

				delete(s.Nodes, key)

				resp := Response{DELETE, key, node.Value, "", true, node.ExpireTime, 0}

				msg, err := json.Marshal(resp)

				notify(resp)

				// notify the messager
				if s.messager != nil && err == nil {

					*s.messager <- string(msg)
				}

				return

			}

		case updateTime := <-update:
			//update duration
			// if the node become a permanent one, the go routine is
			// not needed
			if updateTime.Equal(PERMANENT) {
				fmt.Println("permanent")
				return
			}
			// update duration
			duration = updateTime.Sub(time.Now())
		}
	}
}

// get the value of the key
func Get(key string) Response {
	key = path.Clean(key)

	node, ok := s.Nodes[key]

	if ok {
		return Response{GET, key, node.Value, node.Value, true, node.ExpireTime, 0}
	} else {
		return Response{GET, key, "", "", false, time.Unix(0, 0), 0}
	}
}

// delete the key
func Delete(key string, index uint64) ([]byte, error) {
	key = path.Clean(key)

	node, ok := s.Nodes[key]

	if ok {

		if node.ExpireTime.Equal(PERMANENT) {

			delete(s.Nodes, key)

		} else {

			// kill the expire go routine
			node.update <- PERMANENT
			delete(s.Nodes, key)

		}

		resp := Response{DELETE, key, node.Value, "", true, node.ExpireTime, index}

		msg, err := json.Marshal(resp)

		notify(resp)

		// notify the messager
		if s.messager != nil && err == nil {

			*s.messager <- string(msg)
		}

		return msg, err

	} else {

		return json.Marshal(Response{DELETE, key, "", "", false, time.Unix(0, 0), index})
	}
}

// save the current state of the storage system
func (s *Store) Save() ([]byte, error) {
	b, err := json.Marshal(s)
	if err != nil {
		fmt.Println(err)
		return nil, err
	}
	return b, nil
}

// recovery the state of the stroage system from a previous state
func (s *Store) Recovery(state []byte) error {
	err := json.Unmarshal(state, s)

	// clean the expired nodes
	clean()

	return err
}

// clean all expired keys
func clean() {
	for key, node := range s.Nodes {

		if node.ExpireTime.Equal(PERMANENT) {
			continue
		} else {

			if node.ExpireTime.Sub(time.Now()) >= time.Second {
				node.update = make(chan time.Time)
				go expire(key, node.update, node.ExpireTime)

			} else {
				// we should delete this node
				delete(s.Nodes, key)
			}
		}

	}
}
