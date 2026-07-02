import axios, { type AxiosInstance } from 'axios'

// 自定义类型:拦截器已 unwrap 出 res.data,所以 get/post 等方法返回的实际是 data 本体。
// 通过声明合并覆盖 AxiosInstance,让调用处拿到正确类型,避免到处 as unknown。
// 泛型 R 是响应体类型,默认 any。
interface ApiClient extends AxiosInstance {
  get<T = any>(url: string, config?: Parameters<AxiosInstance['get']>[1]): Promise<T>
  post<T = any>(url: string, data?: any, config?: Parameters<AxiosInstance['post']>[2]): Promise<T>
  put<T = any>(url: string, data?: any, config?: Parameters<AxiosInstance['put']>[2]): Promise<T>
  delete<T = any>(url: string, config?: Parameters<AxiosInstance['delete']>[1]): Promise<T>
}

const api = axios.create({
  baseURL: '/api',
  timeout: 15000,
  headers: { 'Content-Type': 'application/json' },
}) as ApiClient

// Response 拦截器 — unwrap data,统一错误处理。
// 注意:返回 res.data 而非 res,配合上面的 ApiClient 声明让类型对齐。
api.interceptors.response.use(
  (res) => res.data,
  (err) => {
    const msg = err.response?.data?.error || err.message || 'Network Error'
    console.error('[API]', msg)
    return Promise.reject(new Error(msg))
  },
)

export default api
