package gateway

import (
	"fmt"
	"hash/crc32"
	"sort"
	"sync"
)

// ConsistentHash 一致性哈希环
type ConsistentHash struct {
	mu           sync.RWMutex
	hashFunc     func(data []byte) uint32
	replicas     int               // 虚拟节点数量
	keys         []int             // 排序的哈希环
	hashMap      map[int]string    // 虚拟节点 -> 真实节点映射
	nodes        map[string]bool   // 真实节点集合
}

// NewConsistentHash 创建一致性哈希环
// replicas: 每个真实节点对应的虚拟节点数量，建议 150-200
func NewConsistentHash(replicas int, fn func([]byte) uint32) *ConsistentHash {
	if fn == nil {
		fn = crc32.ChecksumIEEE
	}
	return &ConsistentHash{
		replicas: replicas,
		hashFunc: fn,
		hashMap:  make(map[int]string),
		nodes:    make(map[string]bool),
	}
}

// Add 添加节点到哈希环
func (c *ConsistentHash) Add(nodes ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, node := range nodes {
		if c.nodes[node] {
			continue // 节点已存在
		}
		c.nodes[node] = true

		// 为每个真实节点创建多个虚拟节点
		for i := 0; i < c.replicas; i++ {
			// 虚拟节点命名：node#0, node#1, ...
			virtualKey := fmt.Sprintf("%s#%d", node, i)
			hash := int(c.hashFunc([]byte(virtualKey)))
			c.keys = append(c.keys, hash)
			c.hashMap[hash] = node
		}
	}

	// 重新排序哈希环
	sort.Ints(c.keys)
}

// Remove 从哈希环移除节点
func (c *ConsistentHash) Remove(node string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.nodes[node] {
		return // 节点不存在
	}
	delete(c.nodes, node)

	// 移除所有虚拟节点
	for i := 0; i < c.replicas; i++ {
		virtualKey := fmt.Sprintf("%s#%d", node, i)
		hash := int(c.hashFunc([]byte(virtualKey)))
		delete(c.hashMap, hash)

		// 从keys中移除
		idx := c.search(hash)
		if idx < len(c.keys) && c.keys[idx] == hash {
			c.keys = append(c.keys[:idx], c.keys[idx+1:]...)
		}
	}
}

// Get 根据key获取对应的节点
func (c *ConsistentHash) Get(key string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if len(c.keys) == 0 {
		return ""
	}

	// 计算key的哈希值
	hash := int(c.hashFunc([]byte(key)))

	// 二分查找第一个 >= hash 的虚拟节点
	idx := c.search(hash)

	// 如果超出范围，回到环的起点
	if idx >= len(c.keys) {
		idx = 0
	}

	// 返回虚拟节点对应的真实节点
	return c.hashMap[c.keys[idx]]
}

// GetN 获取key对应的N个不同节点（用于副本/备份）
func (c *ConsistentHash) GetN(key string, n int) []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if len(c.keys) == 0 {
		return nil
	}

	if n > len(c.nodes) {
		n = len(c.nodes)
	}

	hash := int(c.hashFunc([]byte(key)))
	idx := c.search(hash)

	result := make([]string, 0, n)
	seen := make(map[string]bool)

	// 沿着哈希环查找N个不同的真实节点
	for i := 0; i < len(c.keys) && len(result) < n; i++ {
		currentIdx := (idx + i) % len(c.keys)
		node := c.hashMap[c.keys[currentIdx]]
		if !seen[node] {
			seen[node] = true
			result = append(result, node)
		}
	}

	return result
}

// Nodes 返回所有真实节点列表
func (c *ConsistentHash) Nodes() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	nodes := make([]string, 0, len(c.nodes))
	for node := range c.nodes {
		nodes = append(nodes, node)
	}
	return nodes
}

// Size 返回真实节点数量
func (c *ConsistentHash) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.nodes)
}

// search 二分查找第一个 >= hash 的索引
func (c *ConsistentHash) search(hash int) int {
	return sort.Search(len(c.keys), func(i int) bool {
		return c.keys[i] >= hash
	})
}
