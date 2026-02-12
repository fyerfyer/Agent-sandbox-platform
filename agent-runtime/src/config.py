import os
from pathlib import Path
from typing import Any, Optional, Tuple, Type

from pydantic import model_validator
from pydantic_settings import BaseSettings, PydanticBaseSettingsSource, SettingsConfigDict


class YamlSettingsSource(PydanticBaseSettingsSource):
  _YAML_CANDIDATES = ("config.yaml", "config.yml")

  def __init__(self, settings_cls: Type[BaseSettings]):
    super().__init__(settings_cls)
    self._yaml_data: dict = {}
    for name in self._YAML_CANDIDATES:
      path = Path(name)
      if path.is_file():
        try:
          import yaml
          with open(path, "r", encoding="utf-8") as f:
            data = yaml.safe_load(f)
          if isinstance(data, dict):
            self._yaml_data = {k.upper(): v for k, v in data.items()}
        except ImportError:
          pass
        except Exception:
          pass
        break

  def get_field_value(
    self, field: Any, field_name: str
  ) -> Tuple[Any, str, bool]:
    val = self._yaml_data.get(field_name.upper())
    return val, field_name, val is None

  def __call__(self) -> dict[str, Any]:
    return {
      k: v
      for k, v in self._yaml_data.items()
      if v is not None
    }


class Settings(BaseSettings):
  model_config = SettingsConfigDict(
    env_file=".env",
    env_file_encoding="utf-8",
    extra="ignore",
  )

  PORT: int = 50051

  DEEPSEEK_API_KEY: str = ""
  DEEPSEEK_BASE_URL: str = "https://api.deepseek.com"
  MODEL_NAME: str = "deepseek-chat"

  MAX_LOOPS: int = 15

  WORKSPACE_DIR: str = "/app/workspace"

  @model_validator(mode="after")
  def _check_api_key(self) -> "Settings":
    if not self.DEEPSEEK_API_KEY:
      raise ValueError(
        "DEEPSEEK_API_KEY is required. "
        "Set it via environment variable, .env file, or config.yaml."
      )
    return self

  @classmethod
  def settings_customise_sources(
    cls,
    settings_cls: Type[BaseSettings],
    init_settings: PydanticBaseSettingsSource,
    env_settings: PydanticBaseSettingsSource,
    dotenv_settings: PydanticBaseSettingsSource,
    file_secret_settings: PydanticBaseSettingsSource,
  ):
    return (
      init_settings,
      env_settings,
      dotenv_settings,
      YamlSettingsSource(settings_cls),
      file_secret_settings,
    )


settings = Settings()

os.environ.setdefault("WORKSPACE_DIR", settings.WORKSPACE_DIR)