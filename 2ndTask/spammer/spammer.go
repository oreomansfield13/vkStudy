package main

import (
	"fmt"
	"sort"
	"sync"
)

func RunPipeline(cmds ...cmd) {
	channels := make([]chan interface{}, len(cmds)+1)
	for i := range channels {
		channels[i] = make(chan interface{})
	}
	close(channels[0])
	var wg sync.WaitGroup
	for i, command := range cmds {
		wg.Add(1)
		go func(command cmd, in, out chan interface{}) {
			defer wg.Done()
			command(in, out)
			func() {
				defer func() { _ = recover() }()
				close(out)
			}()
		}(command, channels[i], channels[i+1])
	}
	go func() {
		for range channels[len(channels)-1] {
		}
	}()
	wg.Wait()
}

func SelectUsers(in, out chan interface{}) {
	var wg sync.WaitGroup
	var mu sync.Mutex
	seen := make(map[uint64]bool)
	for i := range in {
		email, ok := i.(string)
		if !ok {
			continue
		}
		wg.Add(1)
		go func(email string) {
			defer wg.Done()
			user := GetUser(email)
			mu.Lock()
			if !seen[user.ID] {
				seen[user.ID] = true
				mu.Unlock()
				out <- user
			} else {
				mu.Unlock()
			}
		}(email)
	}
	wg.Wait()
}

func SelectMessages(in, out chan interface{}) {
	var wg sync.WaitGroup
	batch := make([]User, 0, GetMessagesMaxUsersBatch)
	point := func() {
		if len(batch) == 0 {
			return
		}
		users := batch
		batch = make([]User, 0, GetMessagesMaxUsersBatch)
		wg.Add(1)
		go func(users []User) {
			defer wg.Done()
			msgs, err := GetMessages(users...)
			if err != nil {
				return
			}
			for _, m := range msgs {
				out <- m
			}
		}(users)
	}
	for i := range in {
		user, ok := i.(User)
		if !ok {
			continue
		}
		batch = append(batch, user)
		if len(batch) == GetMessagesMaxUsersBatch {
			point()
		}
	}
	point()
	wg.Wait()
}

func CheckSpam(in, out chan interface{}) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, HasSpamMaxAsyncRequests)
	for i := range in {
		id, ok := i.(MsgID)
		if !ok {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(id MsgID) {
			defer wg.Done()
			defer func() { <-sem }()
			spam, err := HasSpam(id)
			if err != nil {
				return
			}
			out <- MsgData{ID: id, HasSpam: spam}
		}(id)

	}
	wg.Wait()
}

func CombineResults(in, out chan interface{}) {
	var data []MsgData
	for i := range in {
		md, ok := i.(MsgData)
		if !ok {
			continue
		}
		data = append(data, md)
	}
	sort.Slice(data, func(i, j int) bool {
		if data[i].HasSpam != data[j].HasSpam {
			return data[i].HasSpam
		}
		return data[i].ID < data[j].ID
	})
	for _, md := range data {
		out <- fmt.Sprintf("%v %d", md.HasSpam, md.ID)
	}
}
