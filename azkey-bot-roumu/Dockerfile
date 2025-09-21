# Use Python 3.13 slim image
FROM python:3.13-slim

# Install uv package manager
RUN pip install uv

# Set working directory
WORKDIR /app

# Create non-root user
RUN groupadd -r appuser && useradd -r -g appuser appuser

# Copy project files
COPY pyproject.toml ./
COPY azkey_bot_roumu/ ./azkey_bot_roumu/

# Install dependencies and the application
RUN uv pip install --system -e .

# Create data directory for CSV files and set permissions
RUN mkdir -p /app/data && chown -R appuser:appuser /app

# Switch to non-root user
USER appuser

# Set environment variables
ENV PYTHONPATH=/app
ENV PYTHONUNBUFFERED=1

# Volume for persistent CSV data
VOLUME ["/app/data"]

# Set working directory for data files
WORKDIR /app/data

# Default command (can be overridden)
ENTRYPOINT ["azkey-bot-roumu"]
CMD ["--help"]
