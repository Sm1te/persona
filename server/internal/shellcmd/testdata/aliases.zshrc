# A ~/.zshrc shaped like real launcher aliases; every value is fake.
export PATH="$HOME/.local/bin:$PATH"

# ===== Claude / Codex aliases =====
alias gcc1="ANTHROPIC_BASE_URL=https://gw-one.example.com/api/anthropic ANTHROPIC_AUTH_TOKEN=sk-test-one ANTHROPIC_MODEL=glm-test-5 ANTHROPIC_SMALL_FAST_MODEL=glm-test-5-air claude --dangerously-skip-permissions"
alias kcc='ANTHROPIC_BASE_URL=https://gw-two.example.com/anthropic ANTHROPIC_API_KEY=sk-test-two ANTHROPIC_MODEL="kimi-test-k2[1m]" API_TIMEOUT_MS=600000 claude'
alias dcc=ANTHROPIC_MODEL=deepseek-test\ chat
alias dcc2=CLAUDE_CODE_SUBAGENT_MODEL=deepseek-test-flash\ claude
  alias mcc="export HTTPS_PROXY=http://proxy.example.com:7890; ANTHROPIC_BASE_URL=https://gw-three.example.com ANTHROPIC_AUTH_TOKEN='sk-test-three' ANTHROPIC_CUSTOM_HEADERS=\"X-Test-Team: alpha\" env claude --model minimax-test-m2 --settings ~/.claude/settings.mcc.json" # trailing comment
alias ncodex="OPENAI_API_KEY=sk-test-codex codex --yolo"
alias ncc="/opt/homebrew/bin/claude"
alias ll='ls -la'
# alias old="ANTHROPIC_AUTH_TOKEN=sk-test-commented claude"
alias kcc='ANTHROPIC_BASE_URL=https://gw-two.example.com/anthropic ANTHROPIC_API_KEY=sk-test-two-new ANTHROPIC_MODEL="kimi-test-k2[1m]" claude'
alias -g G='| grep'
