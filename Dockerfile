# Use a lightweight Python base image
FROM python:3.11-slim

# Install tzdata so the OS can handle the timezone settings from the app
RUN apt-get update && apt-get install -y tzdata && rm -rf /var/lib/apt/lists/*

# Set the working directory inside the container
WORKDIR /app

# Copy dependency list and install them
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# Copy the application package and folders
COPY app.py .
COPY logo.png . 
COPY delegatarr/ delegatarr/
COPY static/ static/
COPY templates/ templates/

# Explicitly declare the /config volume for persistent storage
VOLUME /config

# Expose the port that Waitress is serving on
EXPOSE 5555

# Run the application with unbuffered output (-u) so logs appear instantly in Docker
CMD ["python", "-u", "app.py"]