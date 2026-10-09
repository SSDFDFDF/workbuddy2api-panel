/* React 入口：ThemeProvider + DataProvider + AppShell + 视图分发。 */
import { StrictMode } from 'react';
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
  return (
    <ThemeProvider>
      <DataProvider onNeedKey={() => {}}>
        <AppShell>{(view) => <View id={view} />}</AppShell>
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
