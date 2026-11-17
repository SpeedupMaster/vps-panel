export const navItems = [
  { id: 'overview', label: '总览', d: 'M4 4h7v7H4z M13 4h7v7h-7z M4 13h7v7H4z M13 13h7v7h-7z' },
  { id: 'routes', label: '线路', d: 'M4 7h12 M13 4l3 3-3 3 M20 17H8 M11 14l-3 3 3 3' },
  { id: 'topology', label: '拓扑', d: 'M10 5a2 2 0 1 0 4 0a2 2 0 1 0-4 0 M3 19a2 2 0 1 0 4 0a2 2 0 1 0-4 0 M17 19a2 2 0 1 0 4 0a2 2 0 1 0-4 0 M12 7v5 M12 12l-6.5 5.5 M12 12l6.5 5.5' },
  { id: 'latency', label: '延迟', d: 'M3 12h4l3-8 4 16 3-8h4' },
  { id: 'subscription', label: '订阅', d: 'M10 14a4 4 0 0 0 5.7 0l3-3a4 4 0 0 0-5.7-5.7l-1 1 M14 10a4 4 0 0 0-5.7 0l-3 3a4 4 0 0 0 5.7 5.7l1-1' },
  { id: 'updates', label: '更新中心', d: 'M20 12a8 8 0 1 1-2.3-5.7 M20 4v5h-5' },
  { id: 'alerts', label: '告警', d: 'M6 16V11a6 6 0 0 1 12 0v5l2 2H4z M10 20a2 2 0 0 0 4 0' },
  { id: 'tools', label: '工具', d: 'M15 4a5 5 0 0 0-4.6 6.9L4 17.3V20h2.7l6.4-6.4A5 5 0 0 0 20 9l-3 3-3-1-1-3z' },
  { id: 'settings', label: '设置', d: 'M9 12a3 3 0 1 0 6 0a3 3 0 1 0-6 0 M12 2v3 M12 19v3 M2 12h3 M19 12h3 M4.9 4.9L7 7 M17 17l2.1 2.1 M4.9 19.1L7 17 M17 7l2.1-2.1' },
]
// Bottom bar on phones: four destinations plus "more".
export const mobileTabs = ['overview', 'routes', 'latency', 'alerts']
