package chat

import (
	"regexp"
	"strings"
	"sync"
	"time"
)

// SensitiveWordFilter 敏感词过滤器（DFA算法）
type SensitiveWordFilter struct {
	mu       sync.RWMutex
	root     *dfaNode
	patterns map[string]bool // 编译后的正则表达式
}

type dfaNode struct {
	children map[rune]*dfaNode
	isEnd    bool
	keyword  string
}

var (
	// 默认敏感词库
	DefaultSensitiveWords = []string{
		// 政治敏感词（示例，实际需根据业务配置）
		"台独", "分裂", "反动", "颠覆",
		// 色情低俗词（示例）
		"色情", "赌博", "毒品", "枪支",
		// 暴恐词（示例）
		"恐怖", "爆炸", "袭击",
		// 常见违规词（示例）
		"外挂", "作弊", "骗子", "诈骗",
	}
	
	// Unicode区域敏感字符检测正则
	specialCharRegex = regexp.MustCompile(`[\x{1F600}-\x{1F64F}]|[\x{1F300}-\x{1F5FF}]|[\x{1F680}-\x{1F6FF}]|[\x{1F1E0}-\x{1F1FF}]|[\x{2600}-\x{26FF}]|[\x{2700}-\x{27BF}]`)
	
	// URL检测正则
	urlRegex = regexp.MustCompile(`https?://[^\s]+|www\.[^\s]+`)
	
	// 重复字符检测正则（连续相同字符超过3次）
	// 注意：Go正则不支持反向引用，使用代码检测
	repeatCharRegex = regexp.MustCompile(`a{4,}`)
	
	// 数字验证码检测（6位以上连续数字）
	codeRegex = regexp.MustCompile(`\d{6,}`)
)

var (
	globalFilter *SensitiveWordFilter
	filterOnce   sync.Once
)

// GetGlobalFilter 获取全局敏感词过滤器
func GetGlobalFilter() *SensitiveWordFilter {
	filterOnce.Do(func() {
		globalFilter = NewSensitiveWordFilter(DefaultSensitiveWords)
	})
	return globalFilter
}

// NewSensitiveWordFilter 创建敏感词过滤器
func NewSensitiveWordFilter(words []string) *SensitiveWordFilter {
	f := &SensitiveWordFilter{
		root:     newDFANode(),
		patterns: make(map[string]bool),
	}
	
	for _, word := range words {
		f.AddWord(word)
	}
	
	return f
}

// newDFANode 创建DFA节点
func newDFANode() *dfaNode {
	return &dfaNode{
		children: make(map[rune]*dfaNode),
	}
}

// AddWord 添加敏感词
func (f *SensitiveWordFilter) AddWord(word string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	
	if word == "" {
		return
	}
	
	// 转换为小写以支持不区分大小写匹配
	word = strings.ToLower(word)
	runes := []rune(word)
	
	node := f.root
	for _, r := range runes {
		if child, ok := node.children[r]; ok {
			node = child
		} else {
			newNode := newDFANode()
			node.children[r] = newNode
			node = newNode
		}
	}
	node.isEnd = true
	node.keyword = word
}

// AddWords 批量添加敏感词
func (f *SensitiveWordFilter) AddWords(words []string) {
	for _, word := range words {
		f.AddWord(word)
	}
}

// RemoveWord 移除敏感词
func (f *SensitiveWordFilter) RemoveWord(word string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	
	word = strings.ToLower(word)
	runes := []rune(word)
	
	node := f.root
	var path []struct {
		node  *dfaNode
		char  rune
	}
	
	for i, r := range runes {
		if child, ok := node.children[r]; ok {
			path = append(path, struct {
				node *dfaNode
				char rune
			}{node, r})
			node = child
		} else {
			return
		}
		
		if i == len(runes)-1 && node.isEnd {
			node.isEnd = false
			node.keyword = ""
		}
	}
}

// Check 检测文本是否包含敏感词
func (f *SensitiveWordFilter) Check(text string) bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	
	if f.root == nil || len(f.root.children) == 0 {
		return false
	}
	
	text = strings.ToLower(text)
	runes := []rune(text)
	
	for i := 0; i < len(runes); i++ {
		node := f.root
		for j := i; j < len(runes); j++ {
			if child, ok := node.children[runes[j]]; ok {
				node = child
				if node.isEnd {
					return true
				}
			} else {
				break
			}
		}
	}
	
	return false
}

// Filter 过滤敏感词，将敏感词替换为指定字符
func (f *SensitiveWordFilter) Filter(text string, replaceChar rune) string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	
	if f.root == nil || len(f.root.children) == 0 {
		return text
	}
	
	result := []rune(text)
	textLower := strings.ToLower(text)
	runes := []rune(textLower)
	
	// 记录需要替换的位置
	replacements := make(map[int]rune)
	
	for i := 0; i < len(runes); i++ {
		node := f.root
		endPos := -1
		
		for j := i; j < len(runes); j++ {
			if child, ok := node.children[runes[j]]; ok {
				node = child
				if node.isEnd {
					endPos = j
				}
			} else {
				break
			}
		}
		
		if endPos >= i {
			// 标记需要替换的位置
			// replaceLen := endPos - i + 1
			for k := i; k <= endPos; k++ {
				replacements[k] = replaceChar
			}
			i = endPos // 跳过已匹配的字符
		}
	}
	
	// 应用替换
	for pos, char := range replacements {
		result[pos] = char
	}
	
	return string(result)
}

// FindAll 查找所有敏感词位置
func (f *SensitiveWordFilter) FindAll(text string) []SensitiveWordPosition {
	f.mu.RLock()
	defer f.mu.RUnlock()
	
	var positions []SensitiveWordPosition
	textLower := strings.ToLower(text)
	runes := []rune(textLower)
	
	for i := 0; i < len(runes); i++ {
		node := f.root
		startPos := -1
		endPos := -1
		keyword := ""
		
		for j := i; j < len(runes); j++ {
			if child, ok := node.children[runes[j]]; ok {
				node = child
				if node.isEnd {
					startPos = i
					endPos = j
					keyword = node.keyword
				}
			} else {
				break
			}
		}
		
		if startPos >= 0 {
			positions = append(positions, SensitiveWordPosition{
				Start:    startPos,
				End:      endPos,
				Word:     keyword,
				Original: string(runes[startPos : endPos+1]),
			})
		}
	}
	
	return positions
}

// SensitiveWordPosition 敏感词位置信息
type SensitiveWordPosition struct {
	Start    int
	End      int
	Word     string
	Original string
}

// LoadWordsFromFile 从文件加载敏感词（每行一个）
func (f *SensitiveWordFilter) LoadWordsFromFile(filepath string) error {
	// TODO: 实现文件加载
	return nil
}

// SpamDetector 垃圾信息检测器
type SpamDetector struct {
	mu               sync.RWMutex
	repeatThreshold  int           // 重复消息阈值
	frequencyLimit   int           // 频率限制（每秒消息数）
	windowSize       time.Duration // 检测窗口大小
	
	// 玩家消息记录: playerID -> []MessageRecord
	messageHistory map[uint64][]MessageRecord
	
	// 全局消息记录（用于检测刷屏）
	globalHistory []MessageRecord
}

type MessageRecord struct {
	Content   string
	Timestamp time.Time
	Hash      string
}

// NewSpamDetector 创建垃圾信息检测器
func NewSpamDetector() *SpamDetector {
	return &SpamDetector{
		repeatThreshold:  3,                // 连续重复3次视为垃圾
		frequencyLimit:   5,                // 每秒最多5条消息
		windowSize:       10 * time.Second, // 10秒窗口
		messageHistory:   make(map[uint64][]MessageRecord),
		globalHistory:    make([]MessageRecord, 0),
	}
}

// SpamResult 垃圾信息检测结果
type SpamResult struct {
	IsSpam      bool
	Reason      string
	Suggestion  string
}

// Detect 检测消息是否为垃圾信息
func (d *SpamDetector) Detect(playerID uint64, content string) SpamResult {
	d.mu.Lock()
	defer d.mu.Unlock()
	
	now := time.Now()
	
	// 1. 检测重复消息
	if d.isRepeatedMessage(playerID, content) {
		return SpamResult{
			IsSpam:     true,
			Reason:     "消息重复发送",
			Suggestion: "请勿重复发送相同内容",
		}
	}
	
	// 2. 检测发送频率
	if d.isFrequencyExceeded(playerID, now) {
		return SpamResult{
			IsSpam:     true,
			Reason:     "发送频率过高",
			Suggestion: "请降低发送频率",
		}
	}
	
	// 3. 检测刷屏行为（全局）
	if d.isGlobalSpam(content, now) {
		return SpamResult{
			IsSpam:     true,
			Reason:     "疑似刷屏行为",
			Suggestion: "请勿频繁发送相同内容",
		}
	}
	
	// 4. 检测特殊字符滥用
	if d.hasAbnormalSpecialChars(content) {
		return SpamResult{
			IsSpam:     true,
			Reason:     "消息包含异常特殊字符",
			Suggestion: "请使用正常的文字内容",
		}
	}
	
	// 5. 检测联系方式（可能是广告）
	if d.hasContactInfo(content) {
		return SpamResult{
			IsSpam:     true,
			Reason:     "消息可能包含广告或联系方式",
			Suggestion: "禁止发布广告或私人联系方式",
		}
	}
	
	// 6. 检测乱码/火星文
	if d.isGibberish(content) {
		return SpamResult{
			IsSpam:     true,
			Reason:     "消息疑似乱码或火星文",
			Suggestion: "请使用正常文字",
		}
	}
	
	// 记录消息
	d.recordMessage(playerID, content, now)
	
	return SpamResult{IsSpam: false}
}

// isRepeatedMessage 检测重复消息
func (d *SpamDetector) isRepeatedMessage(playerID uint64, content string) bool {
	history, ok := d.messageHistory[playerID]
	if !ok || len(history) < d.repeatThreshold {
		return false
	}
	
	// 获取最近的消息
	recent := history[len(history)-d.repeatThreshold:]
	contentHash := hashContent(content)
	
	count := 0
	for _, record := range recent {
		if record.Hash == contentHash {
			count++
		}
	}
	
	return count >= d.repeatThreshold
}

// isFrequencyExceeded 检测频率是否超限
func (d *SpamDetector) isFrequencyExceeded(playerID uint64, now time.Time) bool {
	history, ok := d.messageHistory[playerID]
	if !ok {
		return false
	}
	
	cutoff := now.Add(-d.windowSize)
	validCount := 0
	
	for _, record := range history {
		if record.Timestamp.After(cutoff) {
			validCount++
		}
	}
	
	return validCount >= d.frequencyLimit
}

// isGlobalSpam 检测全局刷屏
func (d *SpamDetector) isGlobalSpam(content string, now time.Time) bool {
	cutoff := now.Add(-d.windowSize)
	contentHash := hashContent(content)
	
	count := 0
	for _, record := range d.globalHistory {
		if record.Timestamp.After(cutoff) && record.Hash == contentHash {
			count++
		}
	}
	
	// 同一内容全局出现5次以上视为刷屏
	return count >= 5
}

// hasAbnormalSpecialChars 检测异常特殊字符
func (d *SpamDetector) hasAbnormalSpecialChars(content string) bool {
	// 检测特殊符号数量占比
	if len(content) == 0 {
		return false
	}
	
	specialCount := 0
	for _, r := range content {
		if !isAlphanumeric(r) && !isChinese(r) && !isWhitespace(r) {
			specialCount++
		}
	}
	
	ratio := float64(specialCount) / float64(len(content))
	return ratio > 0.5 // 超过50%特殊字符
}

// hasContactInfo 检测联系方式
func (d *SpamDetector) hasContactInfo(content string) bool {
	// 检测手机号
	phoneRegex := regexp.MustCompile(`1[3-9]\d{9}`)
	if phoneRegex.MatchString(content) {
		return true
	}
	
	// 检测QQ号
	qqRegex := regexp.MustCompile(`[1-9]\d{4,10}`)
	if qqRegex.MatchString(content) && strings.Contains(content, "QQ") {
		return true
	}
	
	// 检测微信号（简单检测）
	wechatRegex := regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9_-]{5,19}`)
	if wechatRegex.MatchString(content) && strings.Contains(strings.ToLower(content), "微信") {
		return true
	}
	
	return false
}

// isGibberish 检测乱码/火星文
func (d *SpamDetector) isGibberish(content string) bool {
	// 长度过短不考虑
	if len(content) < 4 {
		return false
	}
	
	// 检测纯数字或纯字母
	isAllDigit := true
	isAllAlpha := true
	isAllPunct := true
	
	for _, r := range content {
		if !isDigit(r) {
			isAllDigit = false
		}
		if !isAlpha(r) {
			isAllAlpha = false
		}
		if !isPunct(r) {
			isAllPunct = false
		}
	}
	
	return isAllDigit || isAllAlpha || isAllPunct
}

// recordMessage 记录消息
func (d *SpamDetector) recordMessage(playerID uint64, content string, now time.Time) {
	record := MessageRecord{
		Content:   content,
		Timestamp: now,
		Hash:      hashContent(content),
	}
	
	// 记录玩家消息
	d.messageHistory[playerID] = append(d.messageHistory[playerID], record)
	
	// 限制历史记录大小
	if len(d.messageHistory[playerID]) > 100 {
		d.messageHistory[playerID] = d.messageHistory[playerID][len(d.messageHistory[playerID])-50:]
	}
	
	// 记录全局消息
	d.globalHistory = append(d.globalHistory, record)
	
	// 清理过期记录
	d.cleanupOldRecords(now)
}

// cleanupOldRecords 清理过期记录
func (d *SpamDetector) cleanupOldRecords(now time.Time) {
	cutoff := now.Add(-d.windowSize * 3)
	
	// 清理玩家历史
	for playerID, history := range d.messageHistory {
		var valid []MessageRecord
		for _, record := range history {
			if record.Timestamp.After(cutoff) {
				valid = append(valid, record)
			}
		}
		if len(valid) == 0 {
			delete(d.messageHistory, playerID)
		} else {
			d.messageHistory[playerID] = valid
		}
	}
	
	// 清理全局历史
	var validGlobal []MessageRecord
	for _, record := range d.globalHistory {
		if record.Timestamp.After(cutoff) {
			validGlobal = append(validGlobal, record)
		}
	}
	d.globalHistory = validGlobal
}

// hashContent 计算内容哈希
func hashContent(content string) string {
	return content // 简化版，实际可用MD5/SHA256
}

// Unicode字符检测辅助函数
func isDigit(r rune) bool {
	return r >= '0' && r <= '9'
}

func isAlpha(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

func isAlphanumeric(r rune) bool {
	return isDigit(r) || isAlpha(r) || isChinese(r)
}

func isChinese(r rune) bool {
	return r >= 0x4E00 && r <= 0x9FFF
}

func isWhitespace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r'
}

func isPunct(r rune) bool {
	return (r >= '!' && r <= '/') || (r >= ':' && r <= '@') || (r >= '[' && r <= '`') || (r >= '{' && r <= '~')
}

// MessageFilter 消息过滤器（整合敏感词+垃圾检测）
type MessageFilter struct {
	sensitiveFilter *SensitiveWordFilter
	spamDetector    *SpamDetector
	enabled         bool
}

// NewMessageFilter 创建消息过滤器
func NewMessageFilter() *MessageFilter {
	return &MessageFilter{
		sensitiveFilter: GetGlobalFilter(),
		spamDetector:    NewSpamDetector(),
		enabled:         true,
	}
}

// FilterResult 过滤结果
type FilterResult struct {
	Passed     bool
	Filtered   string
	HasIllegal bool // 是否包含敏感词
	IsSpam     bool
	SpamReason string
	Suggestion string
	Positions  []SensitiveWordPosition // 敏感词位置
}

// Filter 执行完整过滤
func (f *MessageFilter) Filter(playerID uint64, content string) FilterResult {
	if !f.enabled {
		return FilterResult{Passed: true, Filtered: content}
	}
	
	result := FilterResult{
		Filtered: content,
	}
	
	// 1. 敏感词检测
	hasSensitive := f.sensitiveFilter.Check(content)
	if hasSensitive {
		result.HasIllegal = true
		result.Positions = f.sensitiveFilter.FindAll(content)
	}
	
	// 2. 垃圾信息检测
	spamResult := f.spamDetector.Detect(playerID, content)
	if spamResult.IsSpam {
		result.IsSpam = true
		result.SpamReason = spamResult.Reason
		result.Suggestion = spamResult.Suggestion
	}
	
	// 判断是否通过
	result.Passed = !result.HasIllegal && !result.IsSpam
	
	// 自动过滤敏感词
	if result.HasIllegal {
		result.Filtered = f.sensitiveFilter.Filter(content, '*')
	}
	
	return result
}

// FilterOnly 纯过滤（不检测垃圾信息）
func (f *MessageFilter) FilterOnly(content string) (bool, string, []SensitiveWordPosition) {
	hasSensitive := f.sensitiveFilter.Check(content)
	filtered := f.sensitiveFilter.Filter(content, '*')
	positions := f.sensitiveFilter.FindAll(content)
	
	return hasSensitive, filtered, positions
}

// SetEnabled 设置启用状态
func (f *MessageFilter) SetEnabled(enabled bool) {
	f.enabled = enabled
}

// AddSensitiveWord 添加敏感词
func (f *MessageFilter) AddSensitiveWord(word string) {
	f.sensitiveFilter.AddWord(word)
}

// AddSensitiveWords 批量添加敏感词
func (f *MessageFilter) AddSensitiveWords(words []string) {
	f.sensitiveFilter.AddWords(words)
}

// GetSpamDetector 获取垃圾检测器
func (f *MessageFilter) GetSpamDetector() *SpamDetector {
	return f.spamDetector
}
