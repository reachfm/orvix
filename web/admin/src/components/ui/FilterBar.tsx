import { useState, useEffect, useRef, useCallback } from "react";
import { Search, X } from "lucide-react";

interface FilterBarProps {
  search: { value: string; onChange: (v: string) => void; placeholder?: string };
  children?: React.ReactNode;
  onClear?: () => void;
}

export default function FilterBar({ search, children, onClear }: FilterBarProps) {
  const [local, setLocal] = useState(search.value);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  useEffect(() => { setLocal(search.value); }, [search.value]);

  const debouncedOnChange = useCallback((v: string) => {
    setLocal(v);
    if (timer.current) clearTimeout(timer.current);
    timer.current = setTimeout(() => search.onChange(v), 300);
  }, [search]);

  const handleClear = () => {
    setLocal("");
    search.onChange("");
    if (timer.current) clearTimeout(timer.current);
    onClear?.();
  };

  return (
    <div className="orvix-filter-bar">
      <div className="relative flex-1 min-w-[200px] max-w-sm">
        <Search size={16} className="absolute left-3 top-1/2 -translate-y-1/2 text-[var(--text-muted)]" />
        <input
          type="text"
          value={local}
          onChange={(e) => debouncedOnChange(e.target.value)}
          placeholder={search.placeholder || "Search..."}
          className="orvix-input pl-9"
        />
        {local && (
          <button onClick={handleClear} className="absolute right-3 top-1/2 -translate-y-1/2 text-[var(--text-muted)] hover:text-[var(--text-primary)]">
            <X size={14} />
          </button>
        )}
      </div>
      {children}
    </div>
  );
}
