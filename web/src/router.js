import { createRouter, createWebHashHistory } from 'vue-router'
import { state, loadMe } from './store'

const routes = [
  { path: '/login', name: 'login', component: () => import('./views/Login.vue'), meta: { public: true, title: '登录' } },
  { path: '/offline', name: 'offline', component: () => import('./views/Offline.vue'), meta: { public: true, title: '离线' } },
  {
    path: '/',
    component: () => import('./components/Shell.vue'),
    children: [
      { path: '', name: 'overview', component: () => import('./views/Overview.vue'), meta: { title: '总览' } },
      { path: 'servers/:id', name: 'server', component: () => import('./views/ServerDetail.vue'), meta: { title: '服务器', parent: 'overview' } },
      { path: 'routes', name: 'routes', component: () => import('./views/Routes.vue'), meta: { title: '线路' } },
      { path: 'routes/new', name: 'route-new', component: () => import('./views/RouteWizard.vue'), meta: { title: '新建线路', parent: 'routes' } },
      { path: 'routes/:id/edit', name: 'route-edit', component: () => import('./views/RouteWizard.vue'), meta: { title: '编辑线路', parent: 'routes' } },
      { path: 'topology', name: 'topology', component: () => import('./views/Topology.vue'), meta: { title: '拓扑' } },
      { path: 'latency', name: 'latency', component: () => import('./views/Latency.vue'), meta: { title: '延迟' } },
      { path: 'subscription', name: 'subscription', component: () => import('./views/Subscription.vue'), meta: { title: '订阅' } },
      { path: 'updates', name: 'updates', component: () => import('./views/Updates.vue'), meta: { title: '更新中心' } },
      { path: 'alerts', name: 'alerts', component: () => import('./views/Alerts.vue'), meta: { title: '告警' } },
      { path: 'tools', name: 'tools', component: () => import('./views/Tools.vue'), meta: { title: '工具' } },
      { path: 'settings', name: 'settings', component: () => import('./views/Settings.vue'), meta: { title: '设置' } },
    ],
  },
  { path: '/:p(.*)*', redirect: '/' },
]

const router = createRouter({
  history: createWebHashHistory(),
  routes,
  scrollBehavior: () => ({ top: 0 }),
})

router.beforeEach(async (to) => {
  if (to.meta.public) return true
  if (!state.me) {
    try {
      await loadMe()
    } catch (e) {
      // No network is not "logged out": the session cookie may be fine.
      return { name: e.status === 0 ? 'offline' : 'login', query: { next: to.fullPath } }
    }
  }
  return true
})

router.afterEach((to) => {
  document.title = (to.meta.title ? to.meta.title + ' · ' : '') + 'VPS Panel'
})

export default router
