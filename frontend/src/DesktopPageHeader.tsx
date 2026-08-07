import { ArrowLeft } from "lucide-react";
import { Link } from "react-router-dom";

export function DesktopPageHeader({ title }: { title: string }) {
  return <header className="desktop-page-header">
    <Link className="back-button" to="/" title="返回设备" aria-label="返回设备"><ArrowLeft size={18} /></Link>
    <h1>{title}</h1>
  </header>;
}
