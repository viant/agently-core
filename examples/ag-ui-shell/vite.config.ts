import { defineConfig, loadEnv } from 'vite';
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), 'AGENTLY_');
  const target = env.AGENTLY_BACKEND_URL || 'http://127.0.0.1:8080';
  const proxy = { '/v1/ag-ui': { target, changeOrigin: true, cookieDomainRewrite: '', cookiePathRewrite: '/' } };
  return { server: { proxy }, preview: { proxy } };
});
