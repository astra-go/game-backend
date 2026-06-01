package chat

import (
	"strings"
	"testing"
)

// ==================== 敏感词过滤器测试 ====================

func TestSensitiveWordFilter_Check(t *testing.T) {
	words := []string{"色情", "赌博", "外挂", "骗子"}
	filter := NewSensitiveWordFilter(words)
	
	tests := []struct {
		name     string
		text     string
		expected bool
	}{
		{"包含敏感词", "这个游戏有外挂作弊", true},
		{"不包含敏感词", "这是一个正常聊天内容", false},
		{"部分敏感词", "听说有人在赌博", true},
		{"纯英文字母（无敏感词）", "abcdefgh", false},
		{"空文本", "", false},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := filter.Check(tt.text)
			if result != tt.expected {
				t.Errorf("Check(%q) = %v, expected %v", tt.text, result, tt.expected)
			}
		})
	}
}

func TestSensitiveWordFilter_Filter(t *testing.T) {
	words := []string{"色情", "赌博", "外挂"}
	filter := NewSensitiveWordFilter(words)
	
	tests := []struct {
		name     string
		text     string
		expected string
	}{
		{"过滤敏感词", "这个游戏有外挂", "这个游戏有**"},
		{"不过滤", "这是一个正常聊天内容", "这是一个正常聊天内容"},
		{"多个敏感词", "色情加外挂", "**加**"},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := filter.Filter(tt.text, '*')
			if result != tt.expected {
				t.Errorf("Filter(%q) = %q, expected %q", tt.text, result, tt.expected)
			}
		})
	}
}

func TestSensitiveWordFilter_FindAll(t *testing.T) {
	words := []string{"色情", "外挂"}
	filter := NewSensitiveWordFilter(words)
	
	text := "这个游戏有外挂，还有色情内容"
	positions := filter.FindAll(text)
	
	if len(positions) != 2 {
		t.Errorf("期望找到2个敏感词，实际找到 %d 个", len(positions))
	}
	
	if positions[0].Word != "外挂" {
		t.Errorf("第一个词应为外挂，实际为 %s", positions[0].Word)
	}
	
	if positions[1].Word != "色情" {
		t.Errorf("第二个词应为色情，实际为 %s", positions[1].Word)
	}
}

func TestSensitiveWordFilter_AddWord(t *testing.T) {
	filter := NewSensitiveWordFilter(nil)
	
	// 初始状态
	if filter.Check("测试敏感词") {
		t.Error("初始状态不应包含敏感词")
	}
	
	// 添加敏感词
	filter.AddWord("测试敏感词")
	
	if !filter.Check("测试敏感词") {
		t.Error("添加后应能检测到敏感词")
	}
	
	// 过滤效果
	result := filter.Filter("这是测试敏感词的内容", '*')
	if !strings.Contains(result, "*") {
		t.Error("过滤后应包含*字符")
	}
}

func TestSensitiveWordFilter_RemoveWord(t *testing.T) {
	filter := NewSensitiveWordFilter([]string{"测试词"})
	
	// 确认存在
	if !filter.Check("这是一条测试词") {
		t.Error("敏感词应存在")
	}
	
	// 移除
	filter.RemoveWord("测试词")
	
	// 确认已移除
	if filter.Check("这是一条测试词") {
		t.Error("移除后不应检测到敏感词")
	}
}

func TestSensitiveWordFilter_CaseInsensitive(t *testing.T) {
	filter := NewSensitiveWordFilter([]string{"ABCDE"})
	
	tests := []string{"ABCDE", "abcde", "AbCdE"}
	
	for _, text := range tests {
		if !filter.Check(text) {
			t.Errorf("大小写不敏感测试失败: %s 应被检测", text)
		}
	}
}

func TestGlobalFilter(t *testing.T) {
	filter1 := GetGlobalFilter()
	filter2 := GetGlobalFilter()
	
	// 全局单例
	if filter1 != filter2 {
		t.Error("全局过滤器应为单例")
	}
	
	// 默认词库
	if !filter1.Check("外挂") {
		t.Error("默认词库应包含'外挂'")
	}
}

// ==================== 垃圾信息检测器测试 ====================

func TestSpamDetector_RepeatedMessage(t *testing.T) {
	detector := NewSpamDetector()
	playerID := uint64(12345)
	content := "这是一条测试消息"
	
	// 发送重复消息
	for i := 0; i < 5; i++ {
		result := detector.Detect(playerID, content)
		if i < 3 {
			if result.IsSpam {
				t.Errorf("第%d次发送不应被判定为垃圾", i+1)
			}
		}
	}
	
	// 连续3次后应被检测
	result := detector.Detect(playerID, content)
	if !result.IsSpam {
		t.Error("连续重复应被判定为垃圾信息")
	}
	if result.Reason != "消息重复发送" {
		t.Errorf("原因应为'消息重复发送'，实际为 %s", result.Reason)
	}
}

func TestSpamDetector_FrequencyLimit(t *testing.T) {
	detector := NewSpamDetector()
	detector.frequencyLimit = 3
	detector.windowSize = 10 * 1e9 // 10秒
	
	playerID := uint64(12345)
	
	// 快速发送多条不同消息
	for i := 0; i < 5; i++ {
		result := detector.Detect(playerID, "消息"+string(rune('0'+i)))
		if result.IsSpam {
			t.Logf("第%d条消息被判定为频率超限", i+1)
			break
		}
	}
}

func TestSpamDetector_AbnormalSpecialChars(t *testing.T) {
	detector := NewSpamDetector()
	playerID := uint64(12345)
	
	// 特殊字符过多
	spamContent := "!!!!@#$%^&*()!!!!@#$%^&*()"
	result := detector.Detect(playerID, spamContent)
	
	if !result.IsSpam {
		t.Error("特殊字符过多应被判定为垃圾信息")
	}
	if result.Reason != "消息包含异常特殊字符" {
		t.Errorf("原因应为'消息包含异常特殊字符'，实际为 %s", result.Reason)
	}
}

func TestSpamDetector_ContactInfo(t *testing.T) {
	detector := NewSpamDetector()
	playerID := uint64(12345)
	
	tests := []struct {
		name    string
		content string
		isSpam  bool
	}{
		{"手机号", "我的手机号是13812345678，加我", true},
		{"QQ号", "加我QQ123456789", true},
		{"正常内容", "今天天气真好", false},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := detector.Detect(playerID, tt.content)
			if result.IsSpam != tt.isSpam {
				t.Errorf("内容 %q 判定为垃圾=%v，期望=%v", tt.content, result.IsSpam, tt.isSpam)
			}
		})
	}
}

func TestSpamDetector_Gibberish(t *testing.T) {
	detector := NewSpamDetector()
	playerID := uint64(12345)
	
	tests := []struct {
		name    string
		content string
		isSpam  bool
	}{
		{"纯数字", "1234567890", true},
		{"纯字母", "abcdefghij", true},
		{"正常中文", "今天天气很好", false},
		{"正常混合", "你好123world", false},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := detector.Detect(playerID, tt.content)
			if result.IsSpam != tt.isSpam {
				t.Errorf("内容 %q 判定为垃圾=%v，期望=%v", tt.content, result.IsSpam, tt.isSpam)
			}
		})
	}
}

func TestSpamDetector_NormalMessage(t *testing.T) {
	detector := NewSpamDetector()
	playerID := uint64(12345)
	
	messages := []string{
		"大家好",
		"今天天气不错",
		"有人一起玩游戏吗",
		"刚才那局打得不错",
	}
	
	for _, msg := range messages {
		result := detector.Detect(playerID, msg)
		if result.IsSpam {
			t.Errorf("正常消息 %q 被误判为垃圾: %s", msg, result.Reason)
		}
	}
}

// ==================== 消息过滤器综合测试 ====================


func TestMessageFilter_FilterOnly(t *testing.T) {
	filter := NewMessageFilter()
	filter.sensitiveFilter = NewSensitiveWordFilter([]string{"色情"})
	
	hasIllegal, filtered, positions := filter.FilterOnly("这是色情内容")
	
	if !hasIllegal {
		t.Error("应检测到敏感词")
	}
	
	if strings.Contains(filtered, "色情") {
		t.Error("过滤后不应包含敏感词")
	}
	
	if len(positions) != 1 {
		t.Errorf("应找到1个位置，实际找到 %d 个", len(positions))
	}
}

func TestMessageFilter_SetEnabled(t *testing.T) {
	filter := NewMessageFilter()
	filter.sensitiveFilter = NewSensitiveWordFilter([]string{"外挂"})
	
	playerID := uint64(12345)
	
	// 启用状态
	filter.SetEnabled(true)
	result1 := filter.Filter(playerID, "这个外挂很强")
	if result1.Passed {
		t.Error("启用时应过滤敏感词")
	}
	
	// 禁用状态
	filter.SetEnabled(false)
	result2 := filter.Filter(playerID, "这个外挂很强")
	if !result2.Passed {
		t.Error("禁用时应放行所有消息")
	}
}

func TestMessageFilter_AddSensitiveWords(t *testing.T) {
	filter := NewMessageFilter()
	
	// 初始状态
	if filter.sensitiveFilter.Check("测试敏感词") {
		t.Error("初始状态不应包含敏感词")
	}
	
	// 批量添加
	filter.AddSensitiveWords([]string{"测试敏感词", "另一个敏感词"})
	
	if !filter.sensitiveFilter.Check("这是测试敏感词") {
		t.Error("添加后应能检测到敏感词")
	}
}

// ==================== Unicode辅助函数测试 ====================

func TestUnicodeHelpers(t *testing.T) {
	tests := []struct {
		rune    rune
		isDigit bool
		isAlpha bool
		isCn    bool
	}{
		{'1', true, false, false},
		{'a', false, true, false},
		{'中', false, false, true},
		{' ', false, false, false},
	}
	
	for _, tt := range tests {
		if isDigit(tt.rune) != tt.isDigit {
			t.Errorf("isDigit(%c) 错误", tt.rune)
		}
		if isAlpha(tt.rune) != tt.isAlpha {
			t.Errorf("isAlpha(%c) 错误", tt.rune)
		}
		if isChinese(tt.rune) != tt.isCn {
			t.Errorf("isChinese(%c) 错误", tt.rune)
		}
	}
}

// ==================== 性能测试 ====================

func BenchmarkSensitiveWordFilter_Check(b *testing.B) {
	filter := NewSensitiveWordFilter(DefaultSensitiveWords)
	text := "这是一段包含多个敏感词的测试文本，外挂和赌博，还有色情内容"
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		filter.Check(text)
	}
}

func BenchmarkSensitiveWordFilter_Filter(b *testing.B) {
	filter := NewSensitiveWordFilter(DefaultSensitiveWords)
	text := "这是一段包含多个敏感词的测试文本，外挂和赌博，还有色情内容"
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		filter.Filter(text, '*')
	}
}

func BenchmarkSpamDetector_Detect(b *testing.B) {
	detector := NewSpamDetector()
	playerID := uint64(12345)
	content := "这是一条测试消息"
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		detector.Detect(playerID, content)
	}
}
