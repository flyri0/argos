import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

import {
  type DeviceRecord,
  listDevices,
  renameDevice,
  revokeDevice,
} from "../../auth";
import { Modal } from "../../components/Modal";
import { ApproveDeviceForm } from "./ApproveDeviceForm";

type Dialog =
  | { kind: "approve" }
  | { kind: "rename"; device: DeviceRecord }
  | { kind: "revoke"; device: DeviceRecord }
  | null;

// (d) The settings page listing paired devices (§6.2): rename
// (PATCH /api/devices/:id) and revoke (DELETE /api/devices/:id) each row,
// plus (c)'s "approve a new device" form for confirming another device's
// pairing code. Reachable once this device is trusted (has a token, or is
// on localhost) — see DevicesScreen.
export function DeviceManagementScreen() {
  const { t } = useTranslation();
  const [devices, setDevices] = useState<DeviceRecord[] | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [dialog, setDialog] = useState<Dialog>(null);
  const [renameValue, setRenameValue] = useState("");
  const [renameError, setRenameError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    try {
      const list = await listDevices();
      setDevices(list);
      setLoadError(null);
    } catch {
      setLoadError(t("devices.list.errorGeneric"));
    }
  }, [t]);

  useEffect(() => {
    refresh();
  }, [refresh]);

  async function handleRename(device: DeviceRecord) {
    const trimmed = renameValue.trim();
    if (!trimmed) {
      setRenameError(t("devices.list.nameRequired"));
      return;
    }
    try {
      await renameDevice(device.id, trimmed);
      setDialog(null);
      await refresh();
    } catch {
      setRenameError(t("devices.list.errorGeneric"));
    }
  }

  async function handleRevoke(device: DeviceRecord) {
    try {
      await revokeDevice(device.id);
      setDialog(null);
      await refresh();
    } catch {
      setLoadError(t("devices.list.errorGeneric"));
    }
  }

  function formatLastSeen(lastSeenAt: number | null): string {
    if (lastSeenAt === null) return t("devices.list.lastSeenNever");
    return t("devices.list.lastSeen", {
      time: new Date(lastSeenAt * 1000).toLocaleString(),
    });
  }

  return (
    <section>
      <div>
        <h2>{t("devices.list.title")}</h2>
        <button type="button" onClick={() => setDialog({ kind: "approve" })}>
          {t("devices.list.approveNew")}
        </button>
      </div>

      {loadError && <p role="alert">{loadError}</p>}

      {devices === null && !loadError && <p>{t("app.loading")}</p>}

      {devices !== null && devices.length === 0 && <p>{t("devices.list.empty")}</p>}

      {devices !== null && devices.length > 0 && (
        <table>
          <thead>
            <tr>
              <th>{t("devices.list.name")}</th>
              <th>{t("devices.list.lastSeenLabel")}</th>
              <th aria-hidden="true" />
            </tr>
          </thead>
          <tbody>
            {devices.map((device) => (
              <tr key={device.id}>
                <td>
                  {device.name}
                  {device.revoked_at !== null && ` (${t("devices.list.revokedBadge")})`}
                </td>
                <td>{formatLastSeen(device.last_seen_at)}</td>
                <td>
                  <button
                    type="button"
                    onClick={() => {
                      setRenameValue(device.name);
                      setRenameError(null);
                      setDialog({ kind: "rename", device });
                    }}
                  >
                    {t("devices.list.rename")}
                  </button>
                  {device.revoked_at === null && (
                    <button type="button" onClick={() => setDialog({ kind: "revoke", device })}>
                      {t("devices.list.revoke")}
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {dialog?.kind === "approve" && (
        <Modal onDismiss={() => setDialog(null)}>
          <ApproveDeviceForm
            onApproved={() => {
              setDialog(null);
              refresh();
            }}
            onCancel={() => setDialog(null)}
          />
        </Modal>
      )}

      {dialog?.kind === "rename" && (
        <Modal onDismiss={() => setDialog(null)}>
          <form
            onSubmit={(event) => {
              event.preventDefault();
              handleRename(dialog.device);
            }}
          >
            <h2>{t("devices.list.renameTitle")}</h2>
            <div>
              <label htmlFor="device-name">{t("devices.list.name")}</label>
              <input
                id="device-name"
                value={renameValue}
                onChange={(event) => setRenameValue(event.target.value)}
              />
            </div>
            {renameError && <p role="alert">{renameError}</p>}
            <div>
              <button type="submit">{t("devices.list.save")}</button>
              <button type="button" onClick={() => setDialog(null)}>
                {t("devices.list.cancel")}
              </button>
            </div>
          </form>
        </Modal>
      )}

      {dialog?.kind === "revoke" && (
        <Modal onDismiss={() => setDialog(null)}>
          <p>{t("devices.list.confirmRevoke")}</p>
          <button type="button" onClick={() => handleRevoke(dialog.device)}>
            {t("devices.list.confirm")}
          </button>
          <button type="button" onClick={() => setDialog(null)}>
            {t("devices.list.cancel")}
          </button>
        </Modal>
      )}
    </section>
  );
}
