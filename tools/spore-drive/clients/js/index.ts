import { Service } from "./src/Service";
import { Drive as RemoteDrive } from "./src/Drive";

// Drive runs the local spore-drive service (downloading it if needed) and
// talks to it. Use RemoteDrive to talk to a service that is already running.
class Drive extends RemoteDrive {
  static async local(service: Service = new Service()): Promise<Drive> {
    await service.run();
    return new Drive(`http://localhost:${await service.getPort()}/`);
  }
}

export { Drive, RemoteDrive, Service };
export { Document } from "./src/Document";
export type { FileContent } from "./src/Document";
export {
  TauBinarySource,
  TauLatest,
  TauVersion,
  TauUrl,
  TauPath,
} from "./src/Drive";
export type { PlanOptions, Validation } from "./src/Drive";
export { Course, Hop, Host, Node, Progress } from "./gen/drive/v2/drive_pb";
