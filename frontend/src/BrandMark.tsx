import logoURL from "../../assets/brand/homestack.svg";

export function BrandMark({ className }: { className: string }) {
  return <img className={className} src={logoURL} alt="" aria-hidden="true" draggable="false" />;
}
