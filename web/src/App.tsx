import { useTranslation } from "react-i18next";

function App() {
  const { t } = useTranslation();

  return (
    <main>
      <h1>{t("app.title")}</h1>
      <p>{t("app.tagline")}</p>
    </main>
  );
}

export default App;
