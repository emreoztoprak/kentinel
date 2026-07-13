import { Navigate, Route, Routes } from "react-router-dom";
import Layout from "./components/Layout";
import DashboardPage from "./pages/DashboardPage";
import ResourceListPage from "./pages/ResourceListPage";
import ResourceDetailPage from "./pages/ResourceDetailPage";
import EventsPage from "./pages/EventsPage";
import AssistantPage from "./pages/AssistantPage";
import InsightsPage from "./pages/InsightsPage";
import SettingsPage from "./pages/SettingsPage";
import DocsPage from "./pages/DocsPage";
import { NamespaceProvider } from "./context";

export default function App() {
  return (
    <NamespaceProvider>
      <Layout>
        <Routes>
          <Route path="/" element={<DashboardPage />} />
          <Route path="/assistant" element={<AssistantPage />} />
          <Route path="/insights" element={<InsightsPage />} />
          <Route path="/events" element={<EventsPage />} />
          <Route path="/settings" element={<SettingsPage />} />
          <Route path="/docs" element={<DocsPage />} />
          <Route path="/docs/:slug" element={<DocsPage />} />
          <Route path="/resources/:kind" element={<ResourceListPage />} />
          <Route path="/resources/:kind/:namespace/:name" element={<ResourceDetailPage />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </Layout>
    </NamespaceProvider>
  );
}
