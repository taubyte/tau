// Kind icons.
//
// The icon is declared in the DSL — `Icon("database")` on the kind — and served
// through kinds(), so this file is only the mapping from that name to a glyph. A kind
// that gains an icon in the schema gains it here without an edit; a name with no
// mapping falls back rather than throwing, because a missing glyph is a cosmetic gap
// and not a reason for the page to stop compiling config.
//
// @tabler/icons-react, the set the web console already uses.

import { renderToStaticMarkup } from "react-dom/server";
import {
  IconApps,
  IconArrowsRightLeft,
  IconBolt,
  IconBook,
  IconBox,
  IconBucket,
  IconClock,
  IconCube,
  IconDatabase,
  IconKey,
  IconList,
  IconMessage,
  IconPlug,
  IconPlugConnected,
  IconPoint,
  IconRobot,
  IconRoute,
  IconServer,
  IconSitemap,
  IconSettings,
  IconTerminal2,
  IconWorld,
  IconWorldWww,
  type Icon as TablerIcon,
} from "@tabler/icons-react";

const ICONS: Record<string, TablerIcon> = {
  agent: IconRobot,
  apps: IconApps,
  bolt: IconBolt,
  book: IconBook,
  box: IconBox,
  bucket: IconBucket,
  cable: IconPlugConnected, // tabler has no plain "cable"
  clock: IconClock,
  cube: IconCube,
  database: IconDatabase,
  dns: IconWorldWww,
  drive: IconServer,
  gear: IconSettings,
  globe: IconWorld,
  key: IconKey,
  list: IconList,
  message: IconMessage,
  plug: IconPlug,
  route: IconRoute,
  stream: IconArrowsRightLeft,
  terminal: IconTerminal2,
  workflow: IconSitemap,
};

export function KindIcon({ icon, size = 13 }: { icon: string; size?: number }) {
  const Glyph = ICONS[icon] ?? IconPoint;
  return <Glyph size={size} stroke={1.7} />;
}

// The vocabulary table is vanilla DOM, not React. Rather than keep a second icon
// mapping for it — which is the drift this file exists to avoid — it renders the same
// component to markup. One mapping, two consumers.
export function kindIconMarkup(icon: string, size = 13): string {
  return renderToStaticMarkup(<KindIcon icon={icon} size={size} />);
}
