package utils

var (
	ToolTopicTime    = ToolTopics[0]
	ToolTopicSearch  = ToolTopics[1]
	ToolTopicBrowser = ToolTopics[2]
	ToolTopicFiles   = ToolTopics[3]
	ToolTopicSystem  = ToolTopics[4]
	ToolTopicCrontab = ToolTopics[5]
	ToolTopicWriting = ToolTopics[6]
	ToolTopicMCP     = ToolTopics[7]

	// 工具话题 与上面的一致，便于工具选择时使用
	ToolTopics = []string{"时间", "搜索", "浏览器网页的各种操作", "文件写入", "系统bash命令执行", "定时任务", "写作", "MCP外部工具"}
)
