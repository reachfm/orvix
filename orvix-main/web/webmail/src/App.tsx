import { useState } from "react";
import Sidebar from "./components/Sidebar";
import EmailList from "./components/EmailList";
import ReadingPane from "./components/ReadingPane";
import ComposeModal from "./components/ComposeModal";

type Folder = "inbox" | "sent" | "drafts" | "spam" | "trash" | "archive";

export default function App() {
  const [currentFolder, setCurrentFolder] = useState<Folder>("inbox");
  const [selectedEmailId, setSelectedEmailId] = useState<string | null>(null);
  const [composeOpen, setComposeOpen] = useState(false);

  return (
    <div className="flex h-screen overflow-hidden">
      <Sidebar
        currentFolder={currentFolder}
        onFolderChange={setCurrentFolder}
        onCompose={() => setComposeOpen(true)}
      />
      <EmailList
        folder={currentFolder}
        selectedId={selectedEmailId}
        onSelect={setSelectedEmailId}
      />
      <ReadingPane emailId={selectedEmailId} />
      {composeOpen && <ComposeModal onClose={() => setComposeOpen(false)} />}
    </div>
  );
}
