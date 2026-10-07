import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { App } from './App';
import { AuthProvider } from './auth';
import './styles.css';

const container = document.getElementById('root');
if (container === null) {
  throw new Error('缺少 #root 挂载点');
}

createRoot(container).render(
  <StrictMode>
    {/* 登录状态在根部取一次，三个页面共用。 */}
    <AuthProvider>
      <App />
    </AuthProvider>
  </StrictMode>,
);
