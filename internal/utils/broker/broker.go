// broker/broker.go
package broker

import (
	"sync"
)

type Broker struct {
	subscribers map[string][]chan interface{}
	mu          sync.RWMutex
}

func NewBroker() *Broker {
	return &Broker{
		subscribers: make(map[string][]chan interface{}),
	}
}

func (b *Broker) Subscribe(topic string) <-chan interface{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	ch := make(chan interface{}, 1)
	b.subscribers[topic] = append(b.subscribers[topic], ch)
	return ch
}

func (b *Broker) Unsubscribe(topic string, ch <-chan interface{}) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if chans, ok := b.subscribers[topic]; ok {
		for i, c := range chans {
			if c == ch {
				b.subscribers[topic] = append(chans[:i], chans[i+1:]...)
				close(c)
				break
			}
		}
	}
}

func (b *Broker) Publish(topic string, msg interface{}) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if chans, ok := b.subscribers[topic]; ok {
		for _, ch := range chans {
			ch <- msg
		}
	}
}
