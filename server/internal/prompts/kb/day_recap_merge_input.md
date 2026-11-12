{{template "kb/day_recap_day" .Day}}
{{with .Meetings}}知识库里这一天已知的会议：{{range .}}
{{.N}}. {{if eq .Kind "recordings"}}会议录音{{else if eq .Kind "meetings"}}线上会议{{else}}日程{{end}}「{{.Title}}」{{.From.Format "15:04"}}{{if not .To.IsZero}}–{{.To.Format "15:04"}}{{end}}{{end}}{{else}}知识库里没有这一天的会议。{{end}}{{if .Partial}}
这一天太长，中间有些时段没有整理，下面没有它们的笔记。{{end}}

各段笔记：
{{range .Chunks}}
## {{.From}}–{{.To}}{{with .Summary}}
概况：{{.}}{{end}}{{range .Entries}}
- [{{.FromLine}}–{{.ToLine}}] {{if eq .Kind "meeting"}}（像在开会）{{end}}{{.Title}}{{range .Points}}
  - [{{.Line}}] {{.Text}}{{with .Who}}（{{.}}）{{end}}{{end}}{{end}}{{with .Actions}}
待办：{{range .}}
- [{{.Line}}] {{with .Who}}{{.}}：{{end}}{{.Text}}{{with .By}}（期限：{{.}}）{{end}}{{with .DueDate}}（{{.}}）{{end}}{{end}}{{end}}
{{end}}
