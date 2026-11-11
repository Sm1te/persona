Persona 算好的事实（这次开跑时的快照，数字以此为准，别改也别另编）：
{{with .NorthStar}}- 北极星指标（报告开头先照这几行说：今天的打扰次数和可用率、和昨天同一时段比的变化、数据来源，再接下面的内容；数字原样，未知就说未知）：{{range .}}
  - {{.}}{{end}}
{{end}}{{if .Card}}这些会由 Persona 原样列在发给用户的飞书卡片里（含链接和「通过」按钮），你不用再逐条列。{{else}}这次不会发飞书卡片（网页版里有同样的条目，手机 App 里没有）：请在一两句总述之后，按下面的顺序把它们每件一行列出来，说清要用户做什么、去哪点。{{end}}
{{if .WaitingUnknown}}- 等用户处理的：刚重启，还没读到，不要说「没有」。{{else}}- 等用户处理的 {{.WaitingTotal}} 件{{range .Waiting}}
  - {{.}}{{end}}{{end}}
- 过去 {{.Hours}} 小时没成 {{.FailedTotal}} 件{{range .Failed}}
  - {{.Title}}（{{if .Skipped}}被跳过{{else if .Run}}运行失败{{else}}没开起来{{end}}{{if .Why}}：{{.Why}}{{end}}）{{end}}
- 过去 {{.Hours}} 小时做完 {{.DoneTotal}} 件{{range .Done}}
  - {{.}}{{end}}
- 等用户验收的 {{.AcceptTotal}} 件{{range .Accept}}
  - {{.Line}}{{if .Changed}}（改了：{{.Changed}}）{{end}}{{end}}
{{if .LaterTotal}}- 可以晚点答的问题 {{.LaterTotal}} 件：Agent 留下的、不急的问题，列在早晚报的「可以晚点答」一节里。只说有几件，不要逐条再列，也不要替用户回答。
{{end}}- 正在做的 {{.ProgressTotal}} 件{{range .Progress}}
  - {{.Title}}{{if .Stage}}（阶段：{{.Stage}}）{{end}}{{end}}{{with .Leaders}}
- 各方向（每个主管一行；有工作记账的进度卡工具就发卡，没有才在早晚报里每个方向照着各写一行，卡住的写明卡在哪、谁来解）：{{range .}}
  - {{.Name}}：在办 {{.Open}} 件{{if .Waiting}}，等用户拍板 {{.Waiting}} 件{{end}}{{if .Stuck}}，卡住超过一天 {{.Stuck}} 件{{end}}{{if or .Reached .Doing .NeedsYou}}；主管今天的小结：{{with .Reached}}达成 {{.}}；{{end}}{{with .Doing}}在做 {{.}}；{{end}}{{with .NeedsYou}}要用户做 {{.}}{{end}}{{else}}；主管今天还没写小结{{end}}{{end}}{{end}}{{with .Briefs}}
- 派出去的事里要说的（用用户当初的原话开头，每件一行；没列出来的不需要用户做什么、也没有变化，不要提）：{{range .}}
  - {{if .Words}}{{.Date}} 你说：「{{.Words}}」{{else}}{{.Date}} {{if eq .By "leader"}}主管{{else if eq .By "routine"}}定时任务{{else if eq .By "hub"}}Persona{{else if eq .By "unknown"}}（没记下是谁要的）{{else}}助手{{end}}发起：{{.Goal}}{{end}} → {{.Now}}{{with .Status}}（Agent 自己的说法：{{if eq . "done"}}做完了{{else if eq . "blocked"}}卡住了{{else}}没做完{{end}}）{{end}}{{with .Need}}；要用户：{{.}}{{end}}{{if .Decide}}；等用户拍板{{end}}{{with .Parked}}；搁置中，等你定优先级：{{.}}{{end}}{{if .Overdue}}；已超过约定时间{{end}}{{range .Scope}}；范围调整：{{.}}{{end}}{{end}}{{end}}{{with .BriefsDone}}
- 上次简报以来结束的（只在这一次说，逐条标准的结果照写）：{{range .}}
  - {{if .Words}}{{.Date}} 你说：「{{.Words}}」{{else}}{{.Date}} {{if eq .By "leader"}}主管{{else if eq .By "routine"}}定时任务{{else if eq .By "hub"}}Persona{{else if eq .By "unknown"}}（没记下是谁要的）{{else}}助手{{end}}发起：{{.Goal}}{{end}} → {{if .Cancelled}}取消了{{else}}结束了{{end}}{{with .Now}}（{{.}}）{{end}}{{range .Result}}；「{{.Criterion}}」{{if eq .State "met"}}达成{{else if eq .State "not_met"}}没达成{{else if eq .State "dropped"}}已去掉{{with .Change}}（{{.}}）{{end}}{{else}}没核对{{end}}{{end}}{{range .AlsoDone}}；另外做了：{{.}}{{end}}{{end}}{{end}}{{with .ConfirmationSummary}}
- {{.}}{{range $.Confirmations}}
  - {{.}}{{end}}{{end}}
