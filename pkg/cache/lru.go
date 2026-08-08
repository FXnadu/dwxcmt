package cache

import (
	"container/list"
	"sync"
	"time"
)

type entry struct {
	key   string
	value interface{}
	exp   time.Time // 过期时间；零值表示永不过期
}

// LRU 线程安全的 LRU 缓存，支持 TTL 与前缀删除
type LRU struct {
	mu    sync.RWMutex
	cap   int
	ttl   time.Duration
	items map[string]*list.Element
	list  *list.List
}

// New 构造 LRU 缓存
func New(capacity int, ttl time.Duration) *LRU {
	return &LRU{
		cap:   capacity,
		ttl:   ttl,
		items: make(map[string]*list.Element, capacity),
		list:  list.New(),
	}
}

// Get 读取缓存；过期或不存在返回 false
func (c *LRU) Get(key string) (interface{}, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return nil, false
	}
	e := el.Value.(*entry)
	if !e.exp.IsZero() && time.Now().After(e.exp) {
		c.remove(el)
		return nil, false
	}
	c.list.MoveToFront(el)
	return e.value, true
}

// Set 写入缓存
func (c *LRU) Set(key string, value interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		e := el.Value.(*entry)
		e.value = value
		if c.ttl > 0 {
			e.exp = time.Now().Add(c.ttl)
		} else {
			e.exp = time.Time{}
		}
		c.list.MoveToFront(el)
		return
	}
	e := &entry{key: key, value: value}
	if c.ttl > 0 {
		e.exp = time.Now().Add(c.ttl)
	}
	el := c.list.PushFront(e)
	c.items[key] = el
	if c.list.Len() > c.cap {
		c.remove(c.list.Back())
	}
}

// Delete 删除单个键
func (c *LRU) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.remove(el)
	}
}

// DeletePrefix 删除指定前缀的所有键（用于审核/删除后清空某文章的缓存分页）
func (c *LRU) DeletePrefix(prefix string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, el := range c.items {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			c.remove(el)
		}
	}
}

// Len 当前缓存条目数
func (c *LRU) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.list.Len()
}

func (c *LRU) remove(el *list.Element) {
	e := el.Value.(*entry)
	delete(c.items, e.key)
	c.list.Remove(el)
}
