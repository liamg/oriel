import { useState } from "react";

type Props = { label: string; count?: number };

export function Counter({ label, count = 0 }: Props) {
  const [n, setN] = useState<number>(count);
  return (
    <button className="counter" onClick={() => setN(n + 1)}>
      {label}: {n}
    </button>
  );
}
