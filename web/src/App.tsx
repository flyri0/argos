import { useTranslation } from "react-i18next";

import { AccountsScreen } from "./features/accounts/AccountsScreen";

function App() {
  const { t } = useTranslation();

  return (
    <div>
      <header>
        <h1>{t("app.title")}</h1>
      </header>
      <main>
        <AccountsScreen />
      </main>
    </div>
  );
}

export default App;
