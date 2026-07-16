import 'bootstrap/dist/css/bootstrap.min.css'
import '@fortawesome/fontawesome-free/css/all.min.css'
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
