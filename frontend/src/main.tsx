/* React 入口：ThemeProvider + DataProvider + AppShell + 视图分发。 */
import { StrictMode, useCallback, useState } from 'react';
import { createRoot } from 'react-dom/client';
import './tokens.css';
import { ThemeProvider } from './theme';
import { DataProvider } from './data';
import { AppShell, type ViewId } from './AppShell';
import { ToastHost } from './toast';
import { AccountsView } from './views/AccountsView';
import { UsageView } from './views/UsageView';
import { PackagesView } from './views/PackagesView';
import { TasksCenterView } from './views/TasksCenterView';
import { ModelsView } from './views/ModelsView';
import { ConfigView } from './views/ConfigView';
import { LogsView } from './views/LogsView';

// 注入多色混合官方 Logo 作为 Favicon
function setupFavicon() {
  try {
    let link = document.querySelector<HTMLLinkElement>("link[rel~='icon']");
    if (!link) {
      link = document.createElement('link');
      link.rel = 'icon';
      link.type = 'image/svg+xml';
      document.head.appendChild(link);
    }
    const svg = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 40 40" fill="none"><g clip-path="url(#c)"><rect width="40" height="40" rx="20" fill="url(#bg)"/><g filter="url(#f)"><circle cx="31" cy="9" r="12" fill="#6366F1" fill-opacity="0.85"/><circle cx="34" cy="33" r="13" fill="#EC4899" fill-opacity="0.8"/><circle cx="8" cy="34" r="12" fill="#F59E0B" fill-opacity="0.75"/><circle cx="7" cy="7" r="12" fill="#00E5FF" fill-opacity="0.85"/><circle cx="20" cy="20" r="9" fill="#0EC8A9" fill-opacity="0.65"/></g><path d="M28.59 3.13c.4-.35.42-.37.71-.38.47-.04.9.19 1.62.85 1.7 1.54 4.06 4.71 5.53 7.42l.57 1.05.8.4c.78.39 2.05 1.19 2.58 1.62.24.2.27.2.52.1 1.13-.43 2.74.15 4.16 1.51 1.28 1.23 2.51 3.33 2.98 5.08.07.28.16.89.19 1.34.11 1.58-.4 2.84-1.38 3.42-.2.11-.21.14-.2.64.04 2.37-.6 4.74-1.88 7.04-1.45 2.6-4.03 5.28-7.52 7.81-1.88 1.36-6.32 3.95-8.32 4.86-4.81 2.16-8.66 3-12.01 2.59-2-.25-4.26-1.03-5.6-1.93-.35-.24-.4-.26-.67-.18-1.43.41-3.3-.43-4.9-2.2-.63-.7-1.66-2.44-1.99-3.37-.77-2.17-.62-4.13.4-5.3.27-.3.28-.31.22-.82-.09-.83-.14-2.06-.09-2.85l.03-.74-1.11-1.97C.4 26.04-.69 23.47-1.11 21.5c-.23-1.08-.21-1.56.06-1.91.17-.22.71-.44 1.37-.56 1.66-.29 5.27-.03 9.3.68l.41.07.92-.81c1.52-1.35 2.54-2.11 4.4-3.27 1.95-1.22 4.15-2.22 6.62-3.01l.79-.26.44-1.14c1.56-4.12 3.16-7.16 4.3-8.17l.09-.07zM15.52 24.24c-1.76 1.02-2.65 1.53-3.3 2.1-2.62 2.31-3.6 5.97-2.48 9.28.27.82.78 1.7 1.8 3.47 1.02 1.76 1.53 2.64 2.1 3.29 2.31 2.62 5.97 3.6 9.29 2.49.81-.28 1.7-.79 3.46-1.8l10.15-5.86c1.76-1.02 2.65-1.53 3.3-2.1 2.62-2.32 3.6-5.98 2.48-9.29-.27-.82-.78-1.7-1.8-3.47-1.02-1.76-1.53-2.64-2.1-3.29-2.31-2.62-5.97-3.6-9.29-2.49-.81.28-1.7.79-3.46 1.8l-10.15 5.86z" fill="url(#fg)"/><rect x="16.5" y="31.33" width="4.01" height="8.33" rx="2" transform="rotate(-30 16.5 31.33)" fill="white" fill-opacity="0.95"/><rect x="27.31" y="25.09" width="4.01" height="8.33" rx="2" transform="rotate(-30 27.31 25.09)" fill="white" fill-opacity="0.95"/></g><defs><filter id="f" x="-12" y="-12" width="64" height="64" filterUnits="userSpaceOnUse"><feGaussianBlur stdDeviation="5.5"/></filter><linearGradient id="bg" x1="0" y1="0" x2="40" y2="40" gradientUnits="userSpaceOnUse"><stop offset="0%" stop-color="#00E5FF"/><stop offset="30%" stop-color="#0EC8A9"/><stop offset="62%" stop-color="#6366F1"/><stop offset="85%" stop-color="#EC4899"/><stop offset="100%" stop-color="#F59E0B"/></linearGradient><linearGradient id="fg" x1="14.66" y1="11.08" x2="33.38" y2="43.49" gradientUnits="userSpaceOnUse"><stop stop-color="white" stop-opacity="0.9"/><stop offset="0.45" stop-color="white" stop-opacity="1"/></linearGradient><clipPath id="c"><rect width="40" height="40" rx="20" fill="white"/></clipPath></defs></svg>`;
    link.href = 'data:image/svg+xml,' + encodeURIComponent(svg);
  } catch {
    /* 忽略不支持的环境 */
  }
}
setupFavicon();

function View({ id }: { id: ViewId }) {
  switch (id) {
    case 'accounts':
      return <AccountsView />;
    case 'usage':
      return <UsageView />;
    case 'packages':
      return <PackagesView />;
    case 'taskscenter':
      return <TasksCenterView />;
    case 'models':
      return <ModelsView />;
    case 'config':
      return <ConfigView />;
    case 'logs':
      return <LogsView />;
    default:
      return null;
  }
}

function Root() {
  /* 401 → 密钥门（KeyGate）的状态放在最外层：DataProvider 注册的 401 回调
     需要一个「父组件拥有」的 setter，AppShell 自己 set 不到（它是 DataProvider 的子节点）。 */
  const [needKey, setNeedKey] = useState(false);
  const onNeedKey = useCallback(() => setNeedKey(true), []);
  const onKeyReady = useCallback(() => setNeedKey(false), []);
  return (
    <ThemeProvider>
      <DataProvider onNeedKey={onNeedKey}>
        <AppShell needKey={needKey} onKeyReady={onKeyReady}>
          {(view) => <View id={view} />}
        </AppShell>
      </DataProvider>
      <ToastHost />
    </ThemeProvider>
  );
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <Root />
  </StrictMode>,
);
