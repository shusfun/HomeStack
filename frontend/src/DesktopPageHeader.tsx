import { ArrowLeft } from "lucide-react";
import { Link } from "react-router-dom";
import { IconButton } from "./components/ui";

export function DesktopPageHeader({ title }: { title: string }) {
  return <header className="desktop-page-header">
    <IconButton asChild label="返回设备"><Link to="/"><ArrowLeft size={18} /></Link></IconButton>
    <h1>{title}</h1>
  </header>;
}
