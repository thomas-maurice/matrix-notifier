import './style.css'
// Brand glyphs only (GitHub, Slack) — lucide ships no brand icons. Core CSS +
// brands CSS pulls in just the brands webfont, not the solid/regular sets.
import '@fortawesome/fontawesome-free/css/fontawesome.min.css'
import '@fortawesome/fontawesome-free/css/brands.min.css'
import 'vue3-toastify/dist/index.css'
import { createApp } from 'vue'
import Vue3Toastify from 'vue3-toastify'
import App from './App.vue'

createApp(App)
  .use(Vue3Toastify, {
    position: 'top-right',
    autoClose: 4000,
    theme: 'dark',
    hideProgressBar: false,
  })
  .mount('#app')
