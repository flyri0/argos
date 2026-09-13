import { isLocalOrigin, useDeviceToken } from "../../auth";
import { DeviceManagementScreen } from "./DeviceManagementScreen";
import { PairingScreen } from "./PairingScreen";

// The device half of the Settings tab (see SettingsScreen): the pairing
// flow ((a)/(b)) until this device is trusted, then device management
// ((c)/(d)). Trusted means it already has a token, or it's running on
// localhost, which §6.2 exempts from pairing entirely.
export function DevicesScreen() {
  const token = useDeviceToken();
  const trusted = token !== null || isLocalOrigin();

  return trusted ? <DeviceManagementScreen /> : <PairingScreen />;
}
