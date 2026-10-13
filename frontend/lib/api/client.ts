import axios from "axios";

const api = axios.create({
  baseURL: process.env.NEXT_PUBLIC_API_URL || "http://localhost:8080",
  timeout: 15000,
});

api.interceptors.request.use((config) => {
  if (typeof window !== "undefined") {
    const t = localStorage.getItem("access_token");
    if (t) {
      config.headers.Authorization = `Bearer ${t}`;
    }
  }
  return config;
});

api.interceptors.response.use(
  (res) => res,
  (err) => {
    if (err.response?.status === 401) {
      console.warn("[api] 401 unauthorized");
      // TODO: redirect login di FASE 9
    }
    return Promise.reject(err);
  }
);

export default api;
