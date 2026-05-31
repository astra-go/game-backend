package gateway

import (
	"fmt"
	"testing"
)

func TestConsistentHash_Basic(t *testing.T) {
	hash := NewConsistentHash(3, nil)

	// 添加节点
	hash.Add("node1", "node2", "node3")

	if hash.Size() != 3 {
		t.Errorf("期望3个节点，实际 %d", hash.Size())
	}

	// 测试key分配
	testKeys := []string{"room_1", "room_2", "room_3", "room_4", "room_5"}
	for _, key := range testKeys {
		node := hash.Get(key)
		if node == "" {
			t.Errorf("key %s 未分配到节点", key)
		}
		t.Logf("key=%s -> node=%s", key, node)
	}
}

func TestConsistentHash_AddNode(t *testing.T) {
	hash := NewConsistentHash(150, nil)

	// 初始2个节点
	hash.Add("node1", "node2")

	// 记录1000个key的初始分配
	keys := make([]string, 1000)
	initialMapping := make(map[string]string)
	for i := 0; i < 1000; i++ {
		keys[i] = fmt.Sprintf("room_%d", i)
		initialMapping[keys[i]] = hash.Get(keys[i])
	}

	// 添加第3个节点
	hash.Add("node3")

	// 统计有多少key发生了迁移
	migrated := 0
	for _, key := range keys {
		newNode := hash.Get(key)
		if newNode != initialMapping[key] {
			migrated++
		}
	}

	// 理论上应该只有约 1/3 的key迁移到新节点
	migrationRate := float64(migrated) / float64(len(keys))
	t.Logf("添加节点后迁移率: %.2f%% (%d/%d)", migrationRate*100, migrated, len(keys))

	// 迁移率应该在 20%-40% 之间（理论值33%）
	if migrationRate < 0.2 || migrationRate > 0.4 {
		t.Errorf("迁移率异常: %.2f%%，期望 20%%-40%%", migrationRate*100)
	}
}

func TestConsistentHash_RemoveNode(t *testing.T) {
	hash := NewConsistentHash(150, nil)
	hash.Add("node1", "node2", "node3")

	// 记录初始分配
	keys := make([]string, 1000)
	initialMapping := make(map[string]string)
	for i := 0; i < 1000; i++ {
		keys[i] = fmt.Sprintf("room_%d", i)
		initialMapping[keys[i]] = hash.Get(keys[i])
	}

	// 移除node2
	hash.Remove("node2")

	if hash.Size() != 2 {
		t.Errorf("移除后期望2个节点，实际 %d", hash.Size())
	}

	// 统计迁移
	migrated := 0
	migratedToNode1 := 0
	migratedToNode3 := 0
	for _, key := range keys {
		oldNode := initialMapping[key]
		newNode := hash.Get(key)

		if oldNode == "node2" {
			migrated++
			if newNode == "node1" {
				migratedToNode1++
			} else if newNode == "node3" {
				migratedToNode3++
			}
		} else if oldNode != newNode {
			t.Errorf("key %s 不应该迁移: %s -> %s", key, oldNode, newNode)
		}
	}

	t.Logf("node2的key迁移: node1=%d, node3=%d, 总计=%d", migratedToNode1, migratedToNode3, migrated)

	// 原本在node2的key应该均匀分配到node1和node3
	if migrated == 0 {
		t.Error("node2的key未迁移")
	}
}

func TestConsistentHash_GetN(t *testing.T) {
	hash := NewConsistentHash(150, nil)
	hash.Add("node1", "node2", "node3", "node4")

	// 获取3个副本节点
	nodes := hash.GetN("room_123", 3)

	if len(nodes) != 3 {
		t.Errorf("期望3个节点，实际 %d", len(nodes))
	}

	// 检查节点不重复
	seen := make(map[string]bool)
	for _, node := range nodes {
		if seen[node] {
			t.Errorf("节点重复: %s", node)
		}
		seen[node] = true
	}

	t.Logf("room_123的副本节点: %v", nodes)
}

func TestConsistentHash_Distribution(t *testing.T) {
	hash := NewConsistentHash(150, nil)
	hash.Add("node1", "node2", "node3")

	// 测试10000个key的分布
	distribution := make(map[string]int)
	for i := 0; i < 10000; i++ {
		key := fmt.Sprintf("room_%d", i)
		node := hash.Get(key)
		distribution[node]++
	}

	t.Log("key分布:")
	for node, count := range distribution {
		percentage := float64(count) / 100.0
		t.Logf("  %s: %d (%.2f%%)", node, count, percentage)
	}

	// 每个节点应该分配到约 33% 的key（允许 ±6% 误差）
	for node, count := range distribution {
		percentage := float64(count) / 100.0
		if percentage < 27.0 || percentage > 39.0 {
			t.Errorf("节点 %s 分布不均: %.2f%%，期望 27%%-39%%", node, percentage)
		}
	}
}

func TestConsistentHash_EmptyRing(t *testing.T) {
	hash := NewConsistentHash(150, nil)

	// 空环应该返回空字符串
	node := hash.Get("room_1")
	if node != "" {
		t.Errorf("空环应该返回空字符串，实际: %s", node)
	}

	nodes := hash.GetN("room_1", 3)
	if len(nodes) != 0 {
		t.Errorf("空环GetN应该返回空切片，实际: %v", nodes)
	}
}

func TestConsistentHash_DuplicateAdd(t *testing.T) {
	hash := NewConsistentHash(150, nil)

	// 重复添加同一节点
	hash.Add("node1")
	hash.Add("node1")
	hash.Add("node1")

	if hash.Size() != 1 {
		t.Errorf("重复添加后应该只有1个节点，实际 %d", hash.Size())
	}
}

func BenchmarkConsistentHash_Get(b *testing.B) {
	hash := NewConsistentHash(150, nil)
	hash.Add("node1", "node2", "node3", "node4", "node5")

	keys := make([]string, 1000)
	for i := 0; i < 1000; i++ {
		keys[i] = fmt.Sprintf("room_%d", i)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		hash.Get(keys[i%1000])
	}
}

func BenchmarkConsistentHash_GetN(b *testing.B) {
	hash := NewConsistentHash(150, nil)
	hash.Add("node1", "node2", "node3", "node4", "node5")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		hash.GetN(fmt.Sprintf("room_%d", i), 3)
	}
}
