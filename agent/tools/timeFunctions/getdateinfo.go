package timeFunctions

import (
	"container/list"
	"context"
	"encoding/json"
	"fmt"
	"github.com/xumi30/fullmodel/agent/tools"
	"github.com/xumi30/fullmodel/utils"
	"time"

	"github.com/6tail/lunar-go/calendar"
)

// TimeTool implements the Tool interface for getting the current time
type TimeTool struct{}

func NewTimeTool() tools.Tool {
	return &TimeTool{}
}

// Name returns the name of the tool
func (t *TimeTool) Name() string {
	return "get_date_info_about_almanac"
}

// Description returns a description of what the tool does
// Description returns a description of what the tool does
func (t *TimeTool) Description() string {
	return `传入date获取date对应日期和时间的详细信息，只要问今天适合干什么，问今天的运势，只要关于运气，玄学等等关于气运的都要调用这个接口。
	支持星座、儒略日、干支、生肖、节气、节日、彭祖百忌、吉神(喜神/福神/财神/阳贵神/阴贵神)方位、胎神方位、冲煞、纳音、星宿、八字、五行、十神、建除十二值星、青龙名堂等十二神、黄道日及吉凶、法定节假日及调休等,about almanac`
}

// Parameters returns the parameters that the tool accepts
func (t *TimeTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"date": map[string]interface{}{
				"type":        "string",
				"description": "必要参数，指定查询的日期，格式为'YYYY-MM-DD'，如'2023-01-01'，或者不指定，则默认为当前时间",
			},
		},
		"required": []string{"date"},
	}
}

// listToStringSlice 将 *list.List 转换为 []string
func listToStringSlice(l *list.List) []string {
	if l == nil {
		return []string{}
	}
	result := make([]string, 0, l.Len())
	for e := l.Front(); e != nil; e = e.Next() {
		if str, ok := e.Value.(string); ok {
			result = append(result, str)
		}
	}
	return result
}
func (t *TimeTool) Run(ctx context.Context, input string) (string, error) {
	return NewTimeTool().Execute(ctx, input)
}

// Execute executes the tool with the given arguments
// Execute executes the tool with the given arguments
func (t *TimeTool) Execute(ctx context.Context, args string) (string, error) {
	var params map[string]interface{}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		// 如果不是JSON，直接返回当前时间
		return t.getCurrentTimeInfo(time.Now()), nil
	}
	// 获取date参数
	date, _ := params["date"].(string)
	// 如果指定了日期，解析日期
	var queryDate time.Time
	if date != "" {
		var err error
		// 尝试多种日期格式
		layouts := []string{
			time.RFC3339,                // "2006-01-02T15:04:05Z07:00"
			"2006-01-02T15:04:05+08:00", // 带时区的ISO格式
			"2006-01-02T15:04:05",       // 不带时区的ISO格式
			"2006-01-02 15:04:05",       // 常见格式
			"2006-01-02",                // 仅日期
		}

		for _, layout := range layouts {
			queryDate, err = time.Parse(layout, date)
			if err == nil {
				break
			}
		}

		if err != nil {
			return "", fmt.Errorf("无效的日期格式: %v", err)
		}
	} else {
		queryDate = time.Now()
	}
	return t.getCurrentTimeInfo(queryDate), nil
}

func (t *TimeTool) getCurrentTimeInfo(date time.Time) string {
	// 获取星期几
	weekdays := []string{"星期日", "星期一", "星期二", "星期三", "星期四", "星期五", "星期六"}
	weekday := weekdays[date.Weekday()]

	// 获取节日信息
	holiday := getHoliday(date)

	// 创建农历对象
	lunar := calendar.NewLunarFromSolar(calendar.NewSolarFromDate(date))

	// 构建结构化的时间信息
	result := map[string]interface{}{
		"current_time":  date.Format("2006-01-02 15:04:05"),
		"weekday":       weekday,
		"lunar_date":    fmt.Sprintf("%s年%s%s", lunar.GetYearInGanZhi(), lunar.GetMonthInChinese(), lunar.GetDayInChinese()),
		"zodiac":        lunar.GetYearShengXiao(),
		"year_gan_zhi":  lunar.GetYearInGanZhi(),
		"month_gan_zhi": lunar.GetMonthInGanZhi(),
		"day_gan_zhi":   lunar.GetDayInGanZhi(),
		"almanac":       lunar.ToFullString(),
	}

	// 节气信息
	jieQi := lunar.GetJieQi()
	if jieQi != "" {
		result["solar_term"] = jieQi
	}

	if holiday != "" {
		result["holiday"] = holiday
	}

	// 将结果序列化为JSON
	jsonBytes, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Sprintf("Error marshaling result: %v", err)
	}

	return string(jsonBytes)
}

// getHoliday 返回当前日期的节日信息
func getHoliday(date time.Time) string {
	// 固定日期的节日
	holidays := map[string]string{
		"01-01": "元旦",
		"02-14": "情人节",
		"03-08": "妇女节",
		"03-12": "植树节",
		"04-01": "愚人节",
		"05-01": "劳动节",
		"05-04": "青年节",
		"06-01": "儿童节",
		"07-01": "建党节",
		"08-01": "建军节",
		"09-10": "教师节",
		"10-01": "国庆节",
		"12-25": "圣诞节",
	}

	monthDay := date.Format("01-02")
	if holiday, ok := holidays[monthDay]; ok {
		return holiday
	}

	// 可以添加更多动态节日判断逻辑，如春节、端午节、中秋节等
	// 这里只实现了固定日期的节日

	return ""
}

// getLunarDate 返回农历日期
func getLunarDate(date time.Time) string {
	// 创建公历对象
	solar := calendar.NewSolarFromDate(date)
	// 转换为农历对象
	lunar := calendar.NewLunarFromSolar(solar)

	// 构建农历日期字符串
	return fmt.Sprintf("%s年%s%s",
		lunar.GetYearInGanZhi(),   // 干支年
		lunar.GetMonthInChinese(), // 月份
		lunar.GetDayInChinese())   // 日期
}

func (t *TimeTool) SimpleInfo() map[string]string {
	return utils.SimpleInfoMap(utils.ToolTopicTime, "查询指定公历日期的农历、节气、宜忌、干支生肖等传统历法与民俗信息。")
}

func (t *TimeTool) Results() map[string]interface{} {
	return map[string]interface{}{
		"type":        "object",
		"description": "Comprehensive date information including solar and lunar calendar details, zodiac, festivals, and traditional almanac data",
		"properties": map[string]interface{}{
			"current_time": map[string]interface{}{
				"type":        "string",
				"description": "Current date and time in format 'YYYY-MM-DD HH:MM:SS'",
				"example":     "2024-01-15 10:30:45",
			},
			"weekday": map[string]interface{}{
				"type":        "string",
				"description": "Day of the week in Chinese",
				"example":     "星期一",
			},
			"lunar_date": map[string]interface{}{
				"type":        "string",
				"description": "Lunar calendar date in Chinese format",
				"example":     "甲辰年腊月初五",
			},
			"zodiac": map[string]interface{}{
				"type":        "string",
				"description": "Chinese zodiac animal for the year",
				"example":     "龙",
			},
			"year_gan_zhi": map[string]interface{}{
				"type":        "string",
				"description": "Gan-Zhi (Stem-Branch) representation of the year",
				"example":     "甲辰",
			},
			"month_gan_zhi": map[string]interface{}{
				"type":        "string",
				"description": "Gan-Zhi (Stem-Branch) representation of the month",
				"example":     "丁丑",
			},
			"day_gan_zhi": map[string]interface{}{
				"type":        "string",
				"description": "Gan-Zhi (Stem-Branch) representation of the day",
				"example":     "戊子",
			},
			"almanac": map[string]interface{}{
				"type":        "string",
				"description": "Traditional Chinese almanac information",
				"example":     "甲辰年 丁丑月 戊子日 (冲马 煞南)",
			},
			"solar_term": map[string]interface{}{
				"type":        "string",
				"description": "Solar term information (if applicable)",
				"example":     "小寒",
			},
			"holiday": map[string]interface{}{
				"type":        "string",
				"description": "Holiday information (only present if it's a holiday)",
				"example":     "春节",
			},
		},
	}
}
