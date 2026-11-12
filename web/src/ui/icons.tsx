// Stroke icons in Beautiful UI's grammar: 24-unit viewBox, round caps, currentColor.

const P: Record<string, React.ReactNode> = {
  spark: <path d="M12 2l2.4 7.2L22 12l-7.6 2.8L12 22l-2.4-7.2L2 12l7.6-2.8z" fill="currentColor" stroke="none" />,
  // the running status bar's (Lucide's sparkles, as cindy's)
  sparkles: <g><path d="M9.94 15.5A2 2 0 0 0 8.5 14.06l-6.14-1.58a.5.5 0 0 1 0-.96L8.5 9.94A2 2 0 0 0 9.94 8.5l1.58-6.14a.5.5 0 0 1 .96 0l1.58 6.14a2 2 0 0 0 1.44 1.44l6.14 1.58a.5.5 0 0 1 0 .96l-6.14 1.58a2 2 0 0 0-1.44 1.44l-1.58 6.14a.5.5 0 0 1-.96 0z" /><path d="M20 3v4M22 5h-4M4 17v2M5 18H3" /></g>,
  foldToggle: <path d="m7 4 5 5 5-5M7 20l5-5 5 5" />,
  chevron: <path d="M6 9l6 6 6-6" />,
  pin: <g><path d="M12 17v5" /><path d="M9 10.76V6h6v4.76a2 2 0 0 0 1.11 1.79l1.78.9A2 2 0 0 1 19 15.24V17H5v-1.76a2 2 0 0 1 1.11-1.79l1.78-.9A2 2 0 0 0 9 10.76z" /><path d="M8 2h8v4H8z" /></g>,
  filter: <path d="M3 5h18M6 12h12M10 19h4" />,
  chevronRight: <path d="M9 6l6 6-6 6" />,
  chevronLeft: <path d="M15 18l-6-6 6-6" />,
  // show all the lines hidden between two hunks of a diff
  unfold: <path d="M12 3v7M8.5 6.5L12 3l3.5 3.5M12 21v-7M8.5 17.5L12 21l3.5-3.5" />,
  up: <path d="M12 19V5M5 12l7-7 7 7" />,
  // the phone composer's Expand / Collapse (#5688): arrows out to the corners, and back in
  expand: <path d="M15 3h6v6M9 21H3v-6M21 3l-7 7M3 21l7-7" />,
  collapse: <path d="M4 14h6v6M20 10h-6V4M14 10l7-7M3 21l7-7" />,
  plus: <path d="M12 5v14M5 12h14" />,
  x: <path d="M18 6L6 18M6 6l12 12" />,
  // a hand-off to an Agent (#5096; Lucide's corner-down-right, as cindy's card), the way back from it (corner-up-left),
  // and the card's "opens a page" mark (arrow-up-right)
  cornerDownRight: <path d="M15 10l5 5-5 5M4 4v7a4 4 0 0 0 4 4h12" />,
  cornerUpLeft: <path d="M9 14 4 9l5-5M20 20v-7a4 4 0 0 0-4-4H4" />,
  arrowUpRight: <path d="M7 7h10v10M7 17 17 7" />,
  check: <path d="M20 6L9 17l-5-5" />,
  search: <g><circle cx="11" cy="11" r="7" /><path d="M21 21l-4.3-4.3" /></g>,
  // a prompt with no frame: a command run, and the sidebar's mark for a terminal pane (unlike appWindow, it has no box)
  run: <path d="M4 17l6-5-6-5M12 19h8" />,
  read: <g><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" /><path d="M14 2v6h6" /></g>,
  write: <path d="M17 3a2.8 2.8 0 1 1 4 4L7.5 20.5 2 22l1.5-5.5z" />,
  web: <g><circle cx="12" cy="12" r="10" /><path d="M2 12h20M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z" /></g>,
  agent: <g><rect x="4" y="8" width="16" height="12" rx="3" /><path d="M12 8V4M9 14h.01M15 14h.01" /></g>,
  todo: <g><path d="M9 11l3 3L22 4" /><path d="M21 12v7a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11" /></g>,
  hand: <g><path d="M5 12.5l3.5 3.5L19 5.5" /><path d="M4 19h16" /></g>,
  tool: <path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z" />,
  bell: <g><path d="M6 8a6 6 0 0 1 12 0c0 7 3 9 3 9H3s3-2 3-9" /><path d="M10.3 21a1.94 1.94 0 0 0 3.4 0" /></g>,
  stop: <rect x="6" y="6" width="12" height="12" rx="2.5" fill="currentColor" stroke="none" />,
  clip: <path d="m21.4 11.05-9.19 9.19a6 6 0 0 1-8.49-8.49l8.57-8.57A4 4 0 1 1 18 8.84l-8.59 8.57a2 2 0 0 1-2.83-2.83l8.49-8.48" />,
  home: <g><path d="M3 10.5 12 3l9 7.5V20a1 1 0 0 1-1 1h-5v-6h-6v6H4a1 1 0 0 1-1-1z" /></g>,
  machine: <g><rect x="2" y="4" width="20" height="13" rx="2" /><path d="M8 21h8M12 17v4" /></g>,
  phone: <g><rect x="6" y="2" width="12" height="20" rx="2" /><path d="M11 18h2" /></g>,
  watch: <g><rect x="6" y="7" width="12" height="10" rx="2" /><path d="M9 7V2h6v5M9 17v5h6v-5" /></g>,
  layers: <g><path d="M12 2 2 7l10 5 10-5-10-5z" /><path d="M2 17l10 5 10-5M2 12l10 5 10-5" /></g>,
  // a Claude Code workflow run (#2935, #3434; Lucide's workflow): the same mark wherever one is named, in the ai color,
  // as iOS (flowchart) and Android (TreeStructure) draw it
  workflow: <g><rect x="3" y="3" width="8" height="8" rx="2" /><path d="M7 11v4a2 2 0 0 0 2 2h4" /><rect x="13" y="13" width="8" height="8" rx="2" /></g>,
  chat: <path d="M21 12a8 8 0 0 1-11.6 7.1L3 21l1.9-6.4A8 8 0 1 1 21 12z" />,
  sun: <g><circle cx="12" cy="12" r="4" /><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4" /></g>,
  moon: <path d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z" />,
  monitor: <g><rect x="3" y="4" width="18" height="12" rx="2" /><path d="M8 20h8M12 16v4" /></g>,
  logout: <g><path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4" /><path d="M16 17l5-5-5-5M21 12H9" /></g>,
  copy: <g><rect x="9" y="9" width="12" height="12" rx="2.5" /><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" /></g>,
  quote: <path d="M4 11h5v5H4zM4 11c0-3 1.5-5 4-6M14 11h5v5h-5zM14 11c0-3 1.5-5 4-6" />,
  file: <g><path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8z" /><path d="M14 3v5h5" /></g>,
  folder: <path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z" />,
  git: <g><circle cx="6" cy="6" r="2.5" /><circle cx="6" cy="18" r="2.5" /><circle cx="18" cy="8" r="2.5" /><path d="M6 8.5v7M18 10.5c0 4-6 3-10.5 6" /></g>,
  more: <g fill="currentColor" stroke="none"><circle cx="5" cy="12" r="1.8" /><circle cx="12" cy="12" r="1.8" /><circle cx="19" cy="12" r="1.8" /></g>,
  menu: <path d="M4 7h16M4 12h16M4 17h16" />,
  // "Branch to new chat": a path that splits in two
  fork: <path d="M16 3h5v5M8 3H3v5M12 22v-8.3a4 4 0 0 0-1.17-2.87L3 3M15 9l6-6" />,
  sidebar: <g><rect x="3" y="4" width="18" height="16" rx="2.5" /><path d="M9 4v16" /></g>,
  loop: <path d="M17 2l4 4-4 4M3 11v-1a4 4 0 0 1 4-4h14M7 22l-4-4 4-4M21 13v1a4 4 0 0 1-4 4H3" />,
  // a goal the agent works toward (docs/integrations/agent-goal.md)
  target: <g><circle cx="12" cy="12" r="9" /><circle cx="12" cy="12" r="5" /><circle cx="12" cy="12" r="1.2" fill="currentColor" /></g>,
  gear: <g><circle cx="12" cy="12" r="3" /><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06A1.65 1.65 0 0 0 4.6 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.6a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z" /></g>,
  arrowDown: <path d="M12 5v14M19 12l-7 7-7-7" />,
  arrowUp: <path d="M12 19V5M5 12l7-7 7 7" />,
  image: <g><rect x="3" y="3" width="18" height="18" rx="3" /><circle cx="9" cy="9" r="2" /><path d="m21 15-3.1-3.1a2 2 0 0 0-2.8 0L6 21" /></g>,
  external: <path d="M15 3h6v6M10 14 21 3M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6" />,
  download: <path d="M12 3v12M7 10l5 5 5-5M5 21h14" />,
  retry: <path d="M21 12a9 9 0 1 1-2.64-6.36M21 3v6h-6" />,
  undo: <path d="M9 14 4 9l5-5M4 9h10.5a5.5 5.5 0 0 1 0 11H11" />,
  // a message sent on to another chat (#3899, iOS Theme.Icon.forwardMessage): undo's arrow, turned the other way
  forward: <path d="m15 14 5-5-5-5M20 9H9.5a5.5 5.5 0 0 0 0 11H13" />,
  // the image editor's (#4991, iOS Theme.Icon.redo / crop / rotateLeft / cropRatio): Redo is undo's arrow turned the
  // other way, as forward is; a crop's two corners; a frame turned a quarter left; a tall and a wide frame over each other
  redo: <path d="m15 14 5-5-5-5M20 9H9.5a5.5 5.5 0 0 0 0 11H13" />,
  crop: <path d="M6 2v14a2 2 0 0 0 2 2h14M18 22V8a2 2 0 0 0-2-2H2" />,
  rotateLeft: <g><path d="M20 9V7a2 2 0 0 0-2-2h-6M15 2l-3 3 3 3" /><path d="M20 13v5a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V7a2 2 0 0 1 2-2h2" /></g>,
  ratio: <g><rect x="6" y="2" width="12" height="20" rx="2" /><rect x="2" y="6" width="20" height="12" rx="2" /></g>,
  lock: <g><rect x="4" y="11" width="16" height="10" rx="2" /><path d="M8 11V7a4 4 0 0 1 8 0v4" /></g>,
  // show / hide what a secret field holds while it is typed (Lucide's eye / eye-off)
  eye: <g><path d="M2.06 12.35a1 1 0 0 1 0-.7 10.75 10.75 0 0 1 19.88 0 1 1 0 0 1 0 .7 10.75 10.75 0 0 1-19.88 0" /><circle cx="12" cy="12" r="3" /></g>,
  eyeOff: <g><path d="M10.73 5.08A10.43 10.43 0 0 1 12 5c4.64 0 8.58 2.97 9.94 7.1a1 1 0 0 1 0 .7 10.8 10.8 0 0 1-1.44 2.49" /><path d="M14.08 14.16a3 3 0 0 1-4.24-4.24" /><path d="M17.48 17.5A10.75 10.75 0 0 1 2.06 12.35a1 1 0 0 1 0-.7 10.8 10.8 0 0 1 4.45-5.14" /><path d="m2 2 20 20" /></g>,
  // Generic protection and authorization status.
  shield: <path d="M20 13c0 5-3.5 7.5-7.66 8.95a1 1 0 0 1-.67-.01C7.5 20.5 4 18 4 13V6a1 1 0 0 1 1-1c2 0 4.5-1.2 6.24-2.72a1.17 1.17 0 0 1 1.52 0C14.51 3.81 17 5 19 5a1 1 0 0 1 1 1z" />,
  // the shield with a warning mark: Allow Everything (iOS exclamationmark.shield, #3956)
  shieldAlert: <g><path d="M20 13c0 5-3.5 7.5-7.66 8.95a1 1 0 0 1-.67-.01C7.5 20.5 4 18 4 13V6a1 1 0 0 1 1-1c2 0 4.5-1.2 6.24-2.72a1.17 1.17 0 0 1 1.52 0C14.51 3.81 17 5 19 5a1 1 0 0 1 1 1z" /><path d="M12 8v4M12 16h.01" /></g>,
  key: <g><circle cx="7.5" cy="15.5" r="5.5" /><path d="m21 2-9.6 9.6M15.5 7.5l3 3L22 7l-3-3" /></g>,
  terminal: <g><rect x="2" y="4" width="20" height="16" rx="2.5" /><path d="m6 9 3 3-3 3M12 15h5" /></g>,
  // a desktop app's window, a frame with a title bar: an agent in an app's own chat (the sidebar's source marks; a terminal's is `run`)
  appWindow: <g><rect x="2" y="4" width="20" height="16" rx="2.5" /><path d="M2 9h20M5.5 6.5h.01M8.5 6.5h.01" /></g>,
  inbox: <g><path d="M22 12h-6l-2 3h-4l-2-3H2" /><path d="M5.45 5.11 2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.45-6.89A2 2 0 0 0 16.76 4H7.24a2 2 0 0 0-1.79 1.11z" /></g>,
  clock: <g><circle cx="12" cy="12" r="9" /><path d="M12 7v5l3 2" /></g>,
  book: <g><path d="M4 19.5A2.5 2.5 0 0 1 6.5 17H20" /><path d="M6.5 2H20v20H6.5A2.5 2.5 0 0 1 4 19.5v-15A2.5 2.5 0 0 1 6.5 2z" /></g>,
  list: <path d="M8 6h13M8 12h13M8 18h13M3 6h.01M3 12h.01M3 18h.01" />,
  link: <g><path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71" /><path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71" /></g>,
  // a speaker with sound waves: the alert sound (Settings › Notifications › Alert sound)
  volume: <g><path d="M11 4.7a.7.7 0 0 0-1.2-.5L6.4 7.6a1.4 1.4 0 0 1-1 .4H3a1 1 0 0 0-1 1v6a1 1 0 0 0 1 1h2.4a1.4 1.4 0 0 1 1 .4l3.4 3.4a.7.7 0 0 0 1.2-.5z" /><path d="M16 9a5 5 0 0 1 0 6M19.4 18.4a9 9 0 0 0 0-12.8" /></g>,
  bellOff: <g><path d="M8.7 3A6 6 0 0 1 18 8a21.3 21.3 0 0 0 .6 5M17 17H3s3-2 3-9a4.67 4.67 0 0 1 .3-1.7" /><path d="M10.3 21a1.94 1.94 0 0 0 3.4 0M2 2l20 20" /></g>,
  pencil: <path d="M12 20h9M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4z" />,
  square: <rect x="4" y="4" width="16" height="16" rx="2" />,
  at: <g><circle cx="12" cy="12" r="4" /><path d="M16 8v5a3 3 0 0 0 6 0v-1a10 10 0 1 0-3.92 7.94" /></g>,
  info: <g><circle cx="12" cy="12" r="9" /><path d="M12 16v-4M12 8h.01" /></g>,
  mail: <g><rect x="2" y="4" width="20" height="16" rx="2.5" /><path d="m22 7-10 6L2 7" /></g>,
  compose: <g><path d="M12 3H5a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7" /><path d="M18.4 2.6a1.4 1.4 0 0 1 3 3l-9 9a2 2 0 0 1-.85.5l-2.9.85.85-2.9a2 2 0 0 1 .5-.85z" /></g>,
  translate: <g><path d="m5 8 6 6M4 14l6-6 2-3M2 5h12M7 2h1" /><path d="m22 22-5-10-5 10M14 18h6" /></g>,
  archive: <g><rect x="2" y="3" width="20" height="5" rx="1.5" /><path d="M4 8v11a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8M10 12h4" /></g>,
  send: <path d="M22 2 11 13M22 2l-7 20-4-9-9-4z" />,
  hub: <g><circle cx="12" cy="5" r="2.5" /><circle cx="5" cy="19" r="2.5" /><circle cx="19" cy="19" r="2.5" /><path d="M12 7.5v4M12 11.5 6.5 17M12 11.5l5.5 5.5" /></g>,
  trash: <g><path d="M3 6h18M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6" /></g>,
  play: <path d="M6 4l14 8-14 8z" />,
  mic: <g><rect x="9" y="2.5" width="6" height="12" rx="3" /><path d="M5 11a7 7 0 0 0 14 0M12 18v3.5" /></g>,
  // the terminal's key bar and options
  keyboard: <g><rect x="2" y="5" width="20" height="14" rx="2.5" /><path d="M6 9h.01M10 9h.01M14 9h.01M18 9h.01M6 12.5h.01M18 12.5h.01M8 15.5h8" /></g>,
  bolt: <path d="M13 2 4 14h7l-1 8 9-12h-7z" />,
  paste: <g><rect x="8" y="2" width="8" height="4" rx="1" /><path d="M16 4h2a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2h2" /></g>,
  enter: <path d="M9 10l-5 5 5 5M20 4v7a4 4 0 0 1-4 4H4" />,
  wrap: <g><path d="M3 6h18M3 12h15a3 3 0 1 1 0 6h-4M3 18h7" /><path d="m16 16-2 2 2 2" /></g>,
  arrowLeft: <path d="M19 12H5M12 19l-7-7 7-7" />,
  arrowRight: <path d="M5 12h14M12 5l7 7-7 7" />,
  // the chat's model change divider (Lucide's arrow-left-right, as cindy's)
  arrowLeftRight: <path d="M8 3 4 7l4 4M4 7h16M16 21l4-4-4-4M20 17H4" />,
  textSize: <path d="M4 7V4h16v3M9 20h6M12 4v16" />,
  // an extension's entry on the sidebar (docs/extensions/extensions.md "UI slots" nav_page icons; extension, list, bell and doc reuse the above)
  chart: <path d="M3 3v16a2 2 0 0 0 2 2h16M8 17v-4M13 17V8M18 17v-7" />,
  gauge: <g><path d="M12 14l4-4" /><path d="M3.34 19a10 10 0 1 1 17.32 0" /></g>,
  calendar: <g><rect x="3" y="4" width="18" height="18" rx="2.5" /><path d="M16 2v4M8 2v4M3 10h18" /></g>,
  // a to-do of high priority (#1683; Lucide's flag)
  flag: <g><path d="M4 22V4" /><path d="M4 15s1-1 4-1 5 2 8 2 4-1 4-1V3s-1 1-4 1-5-2-8-2-4 1-4 1" /></g>,
  money: <g><rect x="2" y="6" width="20" height="12" rx="2.5" /><circle cx="12" cy="12" r="2.5" /><path d="M6 12h.01M18 12h.01" /></g>,
  // a row's icons in an extension's view (docs/extensions/extensions.md "Declarative UI" row icon; the rest reuse the above)
  person: <g><circle cx="12" cy="8" r="4" /><path d="M4.5 21a7.5 7.5 0 0 1 15 0" /></g>,
  // a lead's Team (#5180; Lucide's users-round, as cindy's Team tab)
  users: <g><path d="M18 21a8 8 0 0 0-16 0" /><circle cx="10" cy="8" r="5" /><path d="M22 20c0-3.37-2-6.5-4-8a5 5 0 0 0-.45-8.3" /></g>,
  star: <path d="M12 2.8l2.84 5.76 6.36.92-4.6 4.49 1.09 6.33L12 17.31l-5.69 2.99 1.09-6.33-4.6-4.49 6.36-.92z" />,
  // the model panel (#5032, after Cindy's): its rail's Recent and All (Lucide's history and layout-grid), a row's
  // Customize (Lucide's sliders-horizontal)
  history: <g><path d="M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8" /><path d="M3 3v5h5M12 7v5l4 2" /></g>,
  grid: <g><rect x="3" y="3" width="7" height="7" rx="1" /><rect x="14" y="3" width="7" height="7" rx="1" /><rect x="14" y="14" width="7" height="7" rx="1" /><rect x="3" y="14" width="7" height="7" rx="1" /></g>,
  sliders: <path d="M21 4h-7M10 4H3M21 12h-9M8 12H3M21 20h-5M12 20H3M14 2v4M8 10v4M16 18v4" />,
  warning: <g><path d="M10.29 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z" /><path d="M12 9v4M12 17h.01" /></g>,
  tag: <g><path d="M12.59 2.59A2 2 0 0 0 11.17 2H4a2 2 0 0 0-2 2v7.17a2 2 0 0 0 .59 1.41l8.71 8.71a2.43 2.43 0 0 0 3.42 0l6.58-6.58a2.43 2.43 0 0 0 0-3.42z" /><path d="M7.5 7.5h.01" /></g>,
  // Health (docs/assistant/health.md)
  heart: <path d="M19 14c1.49-1.46 3-3.21 3-5.5A5.5 5.5 0 0 0 16.5 3c-1.76 0-3 .5-4.5 2-1.5-1.5-2.74-2-4.5-2A5.5 5.5 0 0 0 2 8.5c0 2.3 1.5 4.05 3 5.5l7 7z" />,
  upload: <g><path d="M12 15V3M7 8l5-5 5 5" /><path d="M4 15v4a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-4" /></g>,
  // Lark emoji reactions (LarkThread)
  smile: <g><circle cx="12" cy="12" r="9" /><path d="M8.5 14.5a4.5 4.5 0 0 0 7 0" /><path d="M9 9.5h.01M15 9.5h.01" /></g>,
  // code hosting (extensions' UI version 4, #2951)
  branch: <g><path d="M6 3v12" /><circle cx="18" cy="6" r="3" /><circle cx="6" cy="18" r="3" /><path d="M18 9a9 9 0 0 1-9 9" /></g>,
  commit: <g><circle cx="12" cy="12" r="3" /><path d="M3 12h6M15 12h6" /></g>,
  code: <path d="m16 18 6-6-6-6M8 6l-6 6 6 6" />,
  pullRequest: <g><circle cx="18" cy="18" r="3" /><circle cx="6" cy="6" r="3" /><path d="M13 6h3a2 2 0 0 1 2 2v7M6 9v12" /></g>,
  merge: <g><circle cx="18" cy="18" r="3" /><circle cx="6" cy="6" r="3" /><path d="M6 21V9a9 9 0 0 0 9 9" /></g>,
  // voice chat (docs/web/web.md "Voice"): the composer's entry (a sound wave), the bar's mute (after Lucide's mic-off) and hang-up
  voice: <path d="M3 10v4M7.5 6v12M12 3v18M16.5 7v10M21 10v4" />,
  micOff: <g><path d="M15 9.34V5.5a3 3 0 0 0-5.68-1.33M9 9v2.5a3 3 0 0 0 5.12 2.12" /><path d="M19 11a7 7 0 0 1-1.07 3.73M5 11a7 7 0 0 0 11.37 5.47M12 18v3.5M2 2l20 20" /></g>,
  // hang up: Lucide's phone handset turned to lie flat, as call apps draw it
  hangUp: <path transform="translate(12 12.6) rotate(135) scale(0.86) translate(-12 -12)" d="M22 16.92v3a2 2 0 0 1-2.18 2 19.79 19.79 0 0 1-8.63-3.07 19.5 19.5 0 0 1-6-6 19.79 19.79 0 0 1-3.07-8.67A2 2 0 0 1 4.11 2h3a2 2 0 0 1 2 1.72 12.84 12.84 0 0 0 .7 2.81 2 2 0 0 1-.45 2.11L8.09 9.91a16 16 0 0 0 6 6l1.27-1.27a2 2 0 0 1 2.11-.45 12.84 12.84 0 0 0 2.81.7A2 2 0 0 1 22 16.92z" />,
  // Settings › Extensions
  extension: <path d="M19.44 7.85c-.05.32.06.65.29.88l1.57 1.57a2.4 2.4 0 0 1 0 3.41l-1.61 1.61a.98.98 0 0 1-.84.28c-.47-.07-.8-.48-.97-.93a2.5 2.5 0 1 0-3.21 3.21c.45.17.86.5.93.97a.98.98 0 0 1-.28.84l-1.61 1.61a2.4 2.4 0 0 1-3.41 0l-1.57-1.57a1.03 1.03 0 0 0-.88-.29c-.49.07-.84.5-1.02.97a2.5 2.5 0 1 1-3.24-3.24c.46-.18.89-.53.97-1.02a1.03 1.03 0 0 0-.29-.88l-1.57-1.57a2.4 2.4 0 0 1 0-3.41L4.23 8.77c.24-.24.58-.35.92-.3.52.08.88.53 1.07 1.01a2.5 2.5 0 1 0 3.26-3.26c-.48-.2-.93-.56-1.01-1.07-.05-.34.06-.68.3-.92l1.53-1.53a2.4 2.4 0 0 1 3.41 0l1.57 1.57c.23.23.56.34.88.29.49-.07.84-.5 1.02-.97a2.5 2.5 0 1 1 3.24 3.24c-.46.18-.89.53-.97 1.02z" />,
  // extensions' declarative UI version 6 (the home's and the markets' meanings; lib/extension-ui.ts NEW_ICONS_6)
  sofa: <g><path d="M20 9V6a2 2 0 0 0-2-2H6a2 2 0 0 0-2 2v3" /><path d="M2 16a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-5a2 2 0 0 0-4 0v1.5H6V11a2 2 0 0 0-4 0z" /><path d="M4 18v2M20 18v2" /></g>,
  light: <g><path d="M15 14c.2-1 .7-1.7 1.5-2.5 1-.9 1.5-2.2 1.5-3.5A6 6 0 0 0 6 8c0 1 .2 2.2 1.5 3.5.7.7 1.3 1.5 1.5 2.5" /><path d="M9 18h6M10 22h4" /></g>,
  power: <g><path d="M12 2v10" /><path d="M18.4 6.6a9 9 0 1 1-12.77.04" /></g>,
  fan: <g><path d="M10.83 13.42 4.8 19.44a2 2 0 0 1-3.32-2.1C3 13 6 11 10 11.5" /><path d="M13.17 10.58 19.2 4.56a2 2 0 0 1 3.32 2.1C21 11 18 13 14 12.5" /><path d="M13.42 13.17 19.44 19.2a2 2 0 0 1-2.1 3.32C13 21 11 18 11.5 14" /><path d="M10.58 10.83 4.56 4.8a2 2 0 0 1 2.1-3.32C11 3 13 6 12.5 10" /><circle cx="12" cy="12" r="1.5" /></g>,
  thermometer: <path d="M14 4v10.54a4 4 0 1 1-4 0V4a2 2 0 0 1 4 0z" />,
  droplet: <path d="M12 22a7 7 0 0 0 7-7c0-2-1-3.9-3-5.5s-3.5-4-4-6.5c-.5 2.5-2 4.9-4 6.5S5 13 5 15a7 7 0 0 0 7 7z" />,
  unlock: <g><rect x="4" y="11" width="16" height="10" rx="2" /><path d="M8 11V7a4 4 0 0 1 7.75-1.4" /></g>,
  door: <g><path d="M13 4h3a2 2 0 0 1 2 2v14M2 20h3M13 20h9" /><path d="M10 12v.01" /><path d="M13 4.56v16.88a.5.5 0 0 1-.6.49l-6.6-1.47A1 1 0 0 1 5 19.5V5.6a1 1 0 0 1 .8-.98l6.6-1.47a.5.5 0 0 1 .6.49z" /></g>,
  window: <g><rect x="3" y="3" width="18" height="18" rx="2" /><path d="M12 3v18M3 12h18" /></g>,
  camera: <g><path d="M14.5 4h-5L7 7H4a2 2 0 0 0-2 2v9a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2V9a2 2 0 0 0-2-2h-3z" /><circle cx="12" cy="13" r="3" /></g>,
  video: <g><path d="m16 13 5.22 3.48a.5.5 0 0 0 .78-.42V7.94a.5.5 0 0 0-.76-.43L16 10.5" /><rect x="2" y="6" width="14" height="12" rx="2" /></g>,
  pause: <g><rect x="6" y="4" width="4" height="16" rx="1" /><rect x="14" y="4" width="4" height="16" rx="1" /></g>,
  speaker: <g><rect x="5" y="2" width="14" height="20" rx="2" /><circle cx="12" cy="14" r="4" /><path d="M12 6h.01" /></g>,
  music: <g><path d="M9 18V5l12-2v13" /><circle cx="6" cy="18" r="3" /><circle cx="18" cy="16" r="3" /></g>,
  tv: <g><rect x="2" y="7" width="20" height="15" rx="2" /><path d="m17 2-5 5-5-5" /></g>,
  plug: <g><path d="M12 22v-5M9 8V2M15 8V2" /><path d="M18 8v5a4 4 0 0 1-4 4h-4a4 4 0 0 1-4-4V8z" /></g>,
  battery: <g><rect x="2" y="7" width="16" height="10" rx="2" /><path d="M22 11v2M6 11v2" /></g>,
  cloud: <path d="M17.5 19H9a7 7 0 1 1 6.71-9h1.79a4.5 4.5 0 1 1 0 9z" />,
  rain: <g><path d="M4 14.9A7 7 0 1 1 15.71 8h1.79a4.5 4.5 0 0 1 2.5 8.24" /><path d="M16 14v6M8 14v6M12 16v6" /></g>,
  snow: <path d="M2 12h20M12 2v20M20 16l-4-4 4-4M4 8l4 4-4 4M16 4l-4 4-4-4M8 20l4-4 4 4" />,
  wind: <path d="M12.8 19.6A2 2 0 1 0 14 16H2M17.5 8a2.5 2.5 0 1 1 2 4H2M9.8 4.4A2 2 0 1 1 11 8H2" />,
  leaf: <g><path d="M11 20A7 7 0 0 1 9.8 6.1C15.5 5 17 4.48 19 2c1 2 2 4.18 2 8 0 5.5-4.78 10-10 10z" /><path d="M2 21c0-3 1.85-5.36 5.08-6C9.5 14.52 12 13 13 12" /></g>,
  car: <g><path d="M19 17h2c.6 0 1-.4 1-1v-3c0-.9-.7-1.7-1.5-1.9C18.7 10.6 16 10 16 10s-1.3-1.4-2.2-2.3c-.5-.4-1.1-.7-1.8-.7H5c-.6 0-1.1.4-1.4.9l-1.4 2.9A3.7 3.7 0 0 0 2 12v4c0 .6.4 1 1 1h2" /><circle cx="7" cy="17" r="2" /><path d="M9 17h6" /><circle cx="17" cy="17" r="2" /></g>,
  location: <g><path d="M20 10c0 6-8 12-8 12s-8-6-8-12a8 8 0 0 1 16 0z" /><circle cx="12" cy="10" r="3" /></g>,
  robot: <g><circle cx="12" cy="12" r="9" /><circle cx="12" cy="12" r="3" /><path d="M7 7.5h.01M3.5 12h2" /></g>,
  wifi: <path d="M12 20h.01M2 8.82a15 15 0 0 1 20 0M5 12.86a10 10 0 0 1 14 0M8.5 16.43a5 5 0 0 1 7 0" />,
  trendUp: <path d="M22 7 13.5 15.5l-5-5L2 17M16 7h6v6" />,
  trendDown: <path d="M22 17 13.5 8.5l-5 5L2 7M16 17h6v-6" />,
  wallet: <g><path d="M19 7V4a1 1 0 0 0-1-1H5a2 2 0 0 0 0 4h15a1 1 0 0 1 1 1v4h-3a2 2 0 0 0 0 4h3a1 1 0 0 0 1-1v-2a1 1 0 0 0-1-1" /><path d="M3 5v14a2 2 0 0 0 2 2h15a1 1 0 0 0 1-1v-4" /></g>,
  bank: <path d="M3 22h18M6 18v-7M10 18v-7M14 18v-7M18 18v-7M12 2l8 5H4z" />,
  minus: <path d="M5 12h14" />,
  share: <g><path d="M4 12v8a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-8" /><path d="m16 6-4-4-4 4M12 2v13" /></g>,
};

export type IconName = keyof typeof P;
/** Capability checks must not confuse an empty SVG with a supported native icon. */
export const supportsIcon = (name: string): boolean => Object.prototype.hasOwnProperty.call(P, name);

export function Icon({
  name,
  size = 15,
  stroke = 1.9,
  className,
  style,
}: {
  name: IconName;
  size?: number;
  stroke?: number;
  className?: string;
  style?: React.CSSProperties;
}) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={stroke}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      className={className}
      style={style}
    >
      {P[name]}
    </svg>
  );
}
